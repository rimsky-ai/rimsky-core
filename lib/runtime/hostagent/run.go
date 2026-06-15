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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// agentVersion is reported to the proxy on Register.
const agentVersion = "v1"

// AnonymousRoutingIdentity is the well-known routing key an agent registers
// under when it runs against an anonymous-mode (unauthenticated bootstrap)
// rimsky deployment, where there is no api-key owner to register against.
// Anonymous instances persist with an empty owner api-key, so the proxy
// cannot match them to an owner-keyed agent; this sentinel is the agreed
// fallback the agent and proxy share so an anonymous-mode instance can still
// resolve to a connected agent. The proxy carries the same literal as
// anonymousRoutingIdentity (cmd/rimsky-host-agent-proxy); the two MUST stay
// in lockstep. @concept: host-agent-proxy
const AnonymousRoutingIdentity = "anonymous"

// reconnectMinBackoff is the floor for host-agent dial retries.
const reconnectMinBackoff = 250 * time.Millisecond

// reconnectMaxBackoff is the ceiling for host-agent dial retries.
const reconnectMaxBackoff = 10 * time.Second

// liveChild tracks one spawned binary for a spawn-id: the OS process, the
// gRPC connection to its local server, and a channel closed when it exits.
type liveChild struct {
	spawnID string
	cmd     *exec.Cmd
	conn    *grpc.ClientConn
	port    int
	exited  <-chan struct{}
}

// agent is the per-connection state shared by the daemon's goroutines and
// the four frame handlers (spawn, dispatch, reap, local-http). One agent is
// created per successful proxy connection and torn down when the stream
// closes.
type agent struct {
	cfg          Config
	localBaseURL string

	// @constraint: gRPC client streams require single-writer; sendMu
	// serializes stream.Send across all goroutines, and sendClosed guards
	// against sending on a torn-down stream.
	sendMu     sync.Mutex
	stream     genv1.HostAgent_ConnectClient
	sendClosed bool

	// @constraint: proxyConn is the gRPC client connection the stream rides;
	// closed on teardown.
	proxyConn *grpc.ClientConn

	childMu  sync.Mutex
	children map[string]*liveChild

	// @constraint: serve joins spawnWG before reapAllOrphans so a spawn that
	// registers its child during the stream-close race is still drained;
	// without the join the child leaks for the connection's lifetime (the
	// next reconnect is a fresh agent).
	spawnWG sync.WaitGroup

	// @constraint: pendingForwards maps a local-HTTP forward_id to the
	// channel awaiting its LocalHttpResponse from the proxy.
	forwardMu       sync.Mutex
	pendingForwards map[string]chan *genv1.LocalHttpResponse

	// @constraint: dispatchCancels maps an in-flight executor dispatch's
	// stream_id to the cancel func for the child's inner Execute stream so
	// an inbound CANCEL frame (the proxy relaying a supervisor-side
	// cancellation) tears the child stream down rather than letting it run
	// to child termination.
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
	// @deliberate: an empty api-key is no longer a hard error in
	// anonymous-mode deployments — the agent substitutes the well-known
	// anonymous routing identity so the existing non-empty-key invariant
	// holds end to end. The proxy's Register handler still rejects a truly
	// empty api_key, and the substituted sentinel is what the proxy
	// resolves an anonymous-owner instance against; a non-empty api-key
	// registers under itself exactly as before.
	if cfg.APIKey == "" {
		cfg.APIKey = AnonymousRoutingIdentity
	}

	// @constraint: bind the local HTTP listener once; the handler is set
	// per connection so each spawned child's callbacks tunnel through the
	// currently-live stream.
	lis, baseURL, err := bindLocalListener(cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("hostagent: bind local listener: %w", err)
	}
	defer lis.Close()

	// @constraint: cur is swapped on each (re)connect; the HTTP handler
	// reads it to find the live stream to forward onto.
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

	// @story: host-agent-control-plane — clear any stale status file from
	// a crash before publishing fresh state, and remove it on shutdown so
	// a `status` reader can't see a phantom `connected:true` after the
	// daemon is gone (status truthfulness).
	clearStatusFile(cfg.StatusFile)
	defer clearStatusFile(cfg.StatusFile)

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
		// @deliberate: reset backoff on a successful connect.
		backoff = reconnectMinBackoff

		curMu.Lock()
		cur = a
		curMu.Unlock()

		// @constraint: publish "connected" only AFTER the RegisterAck
		// handshake succeeded (connectOnce returned non-nil) so a
		// `status` call that wins this race sees the live state.
		writeStatusFile(cfg.StatusFile, statusSnapshot{
			Connected: true,
			Proxy:     cfg.RimskyURL,
			Since:     time.Now().UTC().Format(time.RFC3339),
		})

		a.serve(ctx)

		curMu.Lock()
		cur = nil
		curMu.Unlock()

		// @constraint: clear the connected sentinel BEFORE reaping so a
		// `status` call mid-reap doesn't observe `connected:true`
		// against a dead stream.
		clearStatusFile(cfg.StatusFile)

		// @constraint: on stream close, orphan all live children, give
		// them ReapGracePeriod to exit, then SIGKILL the stragglers.
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

// statusSnapshot is the JSON shape the daemon writes to Config.StatusFile.
// The shape is part of the `rimsky agent` CLI contract: `agent status`
// reads it and `agent start --proxy` poll-waits for `connected:true` to
// gate the daemonize handshake. Field changes propagate to:
//   - cmd/rimsky/cli/agent.go::readAgentStatus
//   - examples/host-agent-control-plane-demo.sh (output assertions)
type statusSnapshot struct {
	Connected bool   `json:"connected"`
	Proxy     string `json:"proxy"`
	Since     string `json:"since"`
}

// writeStatusFile atomically writes a JSON snapshot to path. Atomicity
// matters because a `status` reader can race the writer — a partial JSON
// would read as "disconnected" by mistake. Empty path is a no-op.
func writeStatusFile(path string, snap statusSnapshot) {
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		slog.Warn("hostagent: status file mkdir failed", "path", path, "error", err)
		return
	}
	body, err := json.Marshal(snap)
	if err != nil {
		slog.Warn("hostagent: status file marshal failed", "error", err)
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		slog.Warn("hostagent: status file write failed", "path", path, "error", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		slog.Warn("hostagent: status file rename failed", "path", path, "error", err)
	}
}

// clearStatusFile removes the status sentinel, ignoring a missing file.
// Empty path is a no-op.
func clearStatusFile(path string) {
	if path == "" {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		slog.Warn("hostagent: status file remove failed", "path", path, "error", err)
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

	go a.heartbeatLoop(connCtx)

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
			// @constraint: cancel the connection context so in-flight
			// runSpawn goroutines (which dial/handshake under connCtx)
			// unwind, then join them before returning so Run's
			// reapAllOrphans sees every child a racing spawn may have
			// registered. Without this join a spawn that finishes
			// registering after the drain leaks its child for the
			// connection's lifetime.
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
		// @deliberate: liveness only; nothing to do.
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
