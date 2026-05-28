// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// run.go — the host-agent daemon main loop. Run binds the local HTTP
// listener, then repeatedly dials the proxy and serves one HostAgent.Connect
// stream until it closes, reconnecting with backoff. Each connection runs a
// writer (drains a send channel to the stream — gRPC client streams are not
// safe for concurrent Send), a heartbeat goroutine, and a reader loop that
// routes inbound ServerFrames to the spawn/dispatch/reap/http handlers. On
// stream close orphaned children are SIGKILLed after ReapGracePeriod; on
// context cancellation the daemon shuts down cleanly.
//
// @concept: host-agent
package hostagent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os/exec"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// agentVersion is reported to the proxy on Register.
const agentVersion = "v1"

// reconnect backoff bounds.
const (
	reconnectMinBackoff = 250 * time.Millisecond
	reconnectMaxBackoff = 10 * time.Second
)

// liveChild tracks one spawned binary for a spawn-id: the OS process, the
// gRPC connection to its local server, and a channel closed when it exits.
type liveChild struct {
	spawnID string
	cmd     *exec.Cmd
	conn    *grpc.ClientConn
	port    int
	exited  chan struct{}
}

// agent is the per-connection state shared by the daemon's goroutines and
// the four frame handlers (spawn, dispatch, reap, local-http). One agent is
// created per successful proxy connection and torn down when the stream
// closes.
type agent struct {
	cfg          Config
	localBaseURL string // http://<bound-listener-addr>

	// sendMu serializes stream.Send across all goroutines (gRPC client
	// streams require single-writer). sendClosed guards against sending on a
	// torn-down stream.
	sendMu     sync.Mutex
	stream     genv1.HostAgent_ConnectClient
	sendClosed bool

	// proxyConn is the gRPC client connection the stream rides; closed on
	// teardown.
	proxyConn *grpc.ClientConn

	childMu  sync.Mutex
	children map[string]*liveChild // spawn_id → live child

	// spawnWG tracks in-flight runSpawn goroutines. serve joins it before
	// reapAllOrphans so a spawn that registers its child during the
	// stream-close race is still drained (otherwise the child leaks for the
	// connection's lifetime — the next reconnect is a fresh agent).
	spawnWG sync.WaitGroup

	// pendingForwards maps a local-HTTP forward_id to the channel awaiting
	// its LocalHttpResponse from the proxy.
	forwardMu       sync.Mutex
	pendingForwards map[string]chan *genv1.LocalHttpResponse

	// dispatchCancels maps an in-flight executor dispatch's stream_id to the
	// cancel func for the child's inner Execute stream, so an inbound CANCEL
	// frame (the proxy relaying a supervisor-side cancellation) tears the
	// child stream down rather than letting it run to child termination.
	dispatchMu      sync.Mutex
	dispatchCancels map[string]context.CancelFunc
}

// newAgent builds a connection-scoped agent bound to a freshly opened stream.
func newAgent(cfg Config, localBaseURL string, stream genv1.HostAgent_ConnectClient) *agent {
	return &agent{
		cfg:             cfg,
		localBaseURL:    localBaseURL,
		stream:          stream,
		children:        map[string]*liveChild{},
		pendingForwards: map[string]chan *genv1.LocalHttpResponse{},
		dispatchCancels: map[string]context.CancelFunc{},
	}
}

// send writes one ClientFrame to the stream under the single-writer lock.
// Returns false if the stream has been torn down or the send errored.
func (a *agent) send(frame *genv1.ClientFrame) bool {
	a.sendMu.Lock()
	defer a.sendMu.Unlock()
	if a.sendClosed {
		return false
	}
	if err := a.stream.Send(frame); err != nil {
		a.sendClosed = true
		return false
	}
	return true
}

// markSendClosed prevents further sends after the stream tears down.
func (a *agent) markSendClosed() {
	a.sendMu.Lock()
	a.sendClosed = true
	a.sendMu.Unlock()
}

// Run is the host-agent daemon main loop. It binds the local HTTP listener
// once, then dials the proxy and serves one connection at a time, reconnecting
// with exponential backoff until ctx is cancelled. Used by both
// cmd/rimsky-host-agent/main.go and the `rimsky agent start --foreground`
// CLI path.
func Run(ctx context.Context, cfg Config) error {
	cfg = cfg.withDefaults()
	if cfg.RimskyURL == "" {
		return errors.New("hostagent: RIMSKY_URL is required")
	}
	if cfg.APIKey == "" {
		return errors.New("hostagent: RIMSKY_API_KEY is required")
	}

	// 1. Bind the local HTTP listener (Task 48). The handler is set per
	//    connection so each spawned child's callbacks tunnel through the
	//    currently-live stream.
	lis, baseURL, err := bindLocalListener(cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("hostagent: bind local listener: %w", err)
	}
	defer lis.Close()

	// currentAgent is swapped on each (re)connect; the HTTP handler reads it
	// to find the live stream to forward onto.
	var (
		curMu sync.Mutex
		cur   *agent
	)
	httpSrv := &http.Server{Handler: localForwardHandler(func() *agent {
		curMu.Lock()
		defer curMu.Unlock()
		return cur
	})}
	go func() {
		if serveErr := httpSrv.Serve(lis); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			slog.Error("hostagent: local HTTP serve stopped", "error", serveErr)
		}
	}()
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutCtx)
	}()

	slog.Info("hostagent starting", "rimsky_url", cfg.RimskyURL, "agent_label", cfg.AgentLabel, "local_base_url", baseURL)

	backoff := reconnectMinBackoff
	for {
		if ctx.Err() != nil {
			return nil
		}
		a, connErr := connectOnce(ctx, cfg, baseURL)
		if connErr != nil {
			if ctx.Err() != nil {
				return nil
			}
			slog.Warn("hostagent: connect failed; backing off", "error", connErr, "backoff", backoff)
			if !sleepCtx(ctx, backoff) {
				return nil
			}
			backoff = nextBackoff(backoff)
			continue
		}
		backoff = reconnectMinBackoff // reset on a successful connect

		curMu.Lock()
		cur = a
		curMu.Unlock()

		a.serve(ctx)

		curMu.Lock()
		cur = nil
		curMu.Unlock()

		// Stream closed: orphan all live children, give them ReapGracePeriod
		// to exit, then SIGKILL the stragglers.
		a.reapAllOrphans(cfg.ReapGracePeriod)

		if ctx.Err() != nil {
			return nil
		}
		slog.Info("hostagent: stream closed; reconnecting", "backoff", backoff)
		if !sleepCtx(ctx, backoff) {
			return nil
		}
		backoff = nextBackoff(backoff)
	}
}

// connectOnce dials the proxy, opens the Connect stream, sends Register, and
// awaits the RegisterAck. Returns a connection-scoped agent on success.
func connectOnce(ctx context.Context, cfg Config, localBaseURL string) (*agent, error) {
	conn, err := grpc.NewClient(cfg.RimskyURL, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial proxy: %w", err)
	}

	stream, err := genv1.NewHostAgentClient(conn).Connect(ctx)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("open Connect stream: %w", err)
	}

	a := newAgent(cfg, localBaseURL, stream)
	if !a.send(&genv1.ClientFrame{Body: &genv1.ClientFrame_Register{Register: &genv1.Register{
		ApiKey:               cfg.APIKey,
		AgentLabel:           cfg.AgentLabel,
		AgentVersion:         agentVersion,
		LocalCallbackBaseUrl: localBaseURL,
	}}}) {
		_ = conn.Close()
		return nil, errors.New("send Register failed")
	}

	first, err := stream.Recv()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("await RegisterAck: %w", err)
	}
	ack := first.GetRegisterAck()
	if ack == nil {
		_ = conn.Close()
		return nil, fmt.Errorf("expected RegisterAck, got %T", first.GetBody())
	}
	if ack.GetDisplacedPrior() {
		slog.Warn("hostagent: proxy displaced a prior connection for this api-key")
	}
	a.proxyConn = conn
	slog.Info("hostagent connected", "proxy_version", ack.GetProxyVersion())
	return a, nil
}

// serve runs the heartbeat + reader loop for one connection until the stream
// errors/EOFs or ctx is cancelled.
func (a *agent) serve(ctx context.Context) {
	connCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Heartbeat goroutine.
	go a.heartbeatLoop(connCtx)

	// Reader loop: route inbound ServerFrames until the stream ends.
	for {
		frame, err := a.stream.Recv()
		if err != nil {
			if !errors.Is(err, io.EOF) && ctx.Err() == nil {
				slog.Warn("hostagent: stream recv ended", "error", err)
			}
			a.markSendClosed()
			if a.proxyConn != nil {
				_ = a.proxyConn.Close()
			}
			// Cancel the connection context so in-flight runSpawn
			// goroutines (which dial/handshake under connCtx) unwind, then
			// join them before returning so Run's reapAllOrphans sees every
			// child a racing spawn may have registered. Without this join a
			// spawn that finishes registering after the drain leaks its
			// child for the connection's lifetime.
			cancel()
			a.spawnWG.Wait()
			return
		}
		a.routeServerFrame(connCtx, frame)
	}
}

// routeServerFrame dispatches one inbound ServerFrame to its handler. Spawn,
// reap, and dispatch each run out of band so a slow child can't block the
// reader loop; HTTP responses resolve a pending forward inline.
func (a *agent) routeServerFrame(ctx context.Context, frame *genv1.ServerFrame) {
	switch body := frame.GetBody().(type) {
	case *genv1.ServerFrame_HeartbeatAck:
		// liveness only; nothing to do.
	case *genv1.ServerFrame_Spawn:
		a.spawnWG.Add(1)
		go func() {
			defer a.spawnWG.Done()
			a.runSpawn(ctx, body.Spawn)
		}()
	case *genv1.ServerFrame_Reap:
		go a.runReap(body.Reap)
	case *genv1.ServerFrame_DispatchFrame:
		go a.handleDispatchFrame(ctx, body.DispatchFrame)
	case *genv1.ServerFrame_HttpResponse:
		a.deliverHTTPResponse(body.HttpResponse)
	default:
		slog.Warn("hostagent: unknown server frame body", "type", fmt.Sprintf("%T", frame.GetBody()))
	}
}

// heartbeatLoop emits HostAgentHeartbeat frames at the configured cadence
// until the connection context is cancelled or a send fails.
func (a *agent) heartbeatLoop(ctx context.Context) {
	t := time.NewTicker(a.cfg.HeartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if !a.send(&genv1.ClientFrame{Body: &genv1.ClientFrame_Heartbeat{Heartbeat: &genv1.HostAgentHeartbeat{
				SentAtUnixMs: time.Now().UnixMilli(),
			}}}) {
				return
			}
		}
	}
}

// sleepCtx sleeps for d or until ctx is cancelled. Returns false if cancelled.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// nextBackoff doubles the backoff up to the cap.
func nextBackoff(cur time.Duration) time.Duration {
	next := cur * 2
	if next > reconnectMaxBackoff {
		return reconnectMaxBackoff
	}
	return next
}

// bindLocalListener binds the local HTTP listener and returns it plus the
// base URL ("http://host:port") the agent reports in Register. An empty addr
// binds an OS-assigned ephemeral port on 127.0.0.1.
func bindLocalListener(addr string) (net.Listener, string, error) {
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, "", err
	}
	return lis, "http://" + lis.Addr().String(), nil
}
