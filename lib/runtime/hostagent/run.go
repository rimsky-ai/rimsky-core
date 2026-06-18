// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

const agentVersion = "v1"

// @concept: host-agent-proxy
const AnonymousRoutingIdentity = "anonymous"

const reconnectMinBackoff = 250 * time.Millisecond

const reconnectMaxBackoff = 10 * time.Second

type liveChild struct {
	spawnID string
	cmd     *exec.Cmd
	conn    *grpc.ClientConn
	port    int
	exited  <-chan struct{}
}

type agent struct {
	cfg          Config
	localBaseURL string

	sendMu     sync.Mutex
	stream     genv1.HostAgent_ConnectClient
	sendClosed bool

	proxyConn *grpc.ClientConn

	childMu  sync.Mutex
	children map[string]*liveChild

	spawnWG sync.WaitGroup

	forwardMu       sync.Mutex
	pendingForwards map[string]chan *genv1.LocalHttpResponse

	dispatchMu      sync.Mutex
	dispatchCancels map[string]context.CancelFunc
}

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

func (a *agent) markSendClosed() {
	a.sendMu.Lock()
	a.sendClosed = true
	a.sendMu.Unlock()
}

func Run(ctx context.Context, cfg Config) error {
	cfg = cfg.withDefaults()
	if cfg.RimskyURL == "" {
		return errors.New("hostagent: RIMSKY_URL is required")
	}
	if cfg.APIKey == "" {
		cfg.APIKey = AnonymousRoutingIdentity
	}

	lis, baseURL, err := bindLocalListener(cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("hostagent: bind local listener: %w", err)
	}
	defer lis.Close()

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
		backoff = reconnectMinBackoff

		curMu.Lock()
		cur = a
		curMu.Unlock()

		writeStatusFile(cfg.StatusFile, statusSnapshot{
			Connected: true,
			Proxy:     cfg.RimskyURL,
			Since:     time.Now().UTC().Format(time.RFC3339),
		})

		a.serve(ctx)

		curMu.Lock()
		cur = nil
		curMu.Unlock()

		clearStatusFile(cfg.StatusFile)

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

type statusSnapshot struct {
	Connected bool   `json:"connected"`
	Proxy     string `json:"proxy"`
	Since     string `json:"since"`
}

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

func clearStatusFile(path string) {
	if path == "" {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		slog.Warn("hostagent: status file remove failed", "path", path, "error", err)
	}
}

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
			cancel()
			a.spawnWG.Wait()
			return
		}
		a.routeServerFrame(connCtx, frame)
	}
}

func (a *agent) routeServerFrame(ctx context.Context, frame *genv1.ServerFrame) {
	switch body := frame.GetBody().(type) {
	case *genv1.ServerFrame_HeartbeatAck:
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

func nextBackoff(cur time.Duration) time.Duration {
	next := cur * 2
	if next > reconnectMaxBackoff {
		return reconnectMaxBackoff
	}
	return next
}

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
