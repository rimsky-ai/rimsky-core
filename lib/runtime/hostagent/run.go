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
	"sort"
	"sync"
	"time"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const agentVersion = "v1"

// @concept: host-agent-proxy
const AnonymousRoutingIdentity = "anonymous"

const reconnectMinBackoff = 250 * time.Millisecond

const reconnectMaxBackoff = 10 * time.Second

type liveChild struct {
	spawnID     string
	runScopeID  string
	bindingPath string
	cmd         *exec.Cmd
	conn        *grpc.ClientConn
	port        int
	exited      <-chan struct{}
}

type agent struct {
	cfg             Config
	trust           *localTrust
	enrollBaseURL   string
	callbackBaseURL string
	connectedAt     string

	sendMu     sync.Mutex
	stream     genv1.HostAgent_ConnectClient
	sendClosed bool

	proxyConn *grpc.ClientConn

	childMu  sync.Mutex
	children map[string]*liveChild

	tokenMu     sync.Mutex
	spawnTokens map[string]bootstrapToken

	spawnWG sync.WaitGroup

	forwardMu       sync.Mutex
	pendingForwards map[string]chan *genv1.LocalHttpResponse

	dispatchMu      sync.Mutex
	dispatchCancels map[string]context.CancelFunc
}

func newAgent(cfg Config, trust *localTrust, enrollBaseURL, callbackBaseURL string, stream genv1.HostAgent_ConnectClient) *agent {
	return &agent{
		cfg:             cfg,
		trust:           trust,
		enrollBaseURL:   enrollBaseURL,
		callbackBaseURL: callbackBaseURL,
		stream:          stream,
		children:        map[string]*liveChild{},
		spawnTokens:     map[string]bootstrapToken{},
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

	trust, err := newLocalTrust(time.Now())
	if err != nil {
		return fmt.Errorf("hostagent: %w", err)
	}

	var (
		curMu sync.Mutex
		cur   *agent
	)
	currentAgent := func() *agent {
		curMu.Lock()
		defer curMu.Unlock()
		return cur
	}

	enrollLis, enrollBase, err := bindPlaintextListener(cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("hostagent: bind enroll listener: %w", err)
	}
	defer enrollLis.Close()

	callbackLis, callbackBase, err := bindCallbackListener()
	if err != nil {
		return fmt.Errorf("hostagent: bind callback listener: %w", err)
	}
	defer callbackLis.Close()

	enrollSrv := &http.Server{Handler: localEnrollHandler(trust, currentAgent)}
	callbackSrv := &http.Server{
		Handler:   localForwardHandler(currentAgent),
		TLSConfig: trust.callbackServerTLSConfig(),
	}
	go func() {
		if serveErr := enrollSrv.Serve(enrollLis); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			slog.Error("hostagent: enroll HTTP serve stopped", "error", serveErr)
		}
	}()
	go func() {
		if serveErr := callbackSrv.ServeTLS(callbackLis, "", ""); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			slog.Error("hostagent: callback mTLS serve stopped", "error", serveErr)
		}
	}()
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = enrollSrv.Shutdown(shutCtx)
		_ = callbackSrv.Shutdown(shutCtx)
	}()

	slog.Info("hostagent starting", "rimsky_url", cfg.RimskyURL, "agent_label", cfg.AgentLabel, "enroll_base_url", enrollBase, "callback_base_url", callbackBase)

	// @story: host-agent-control-plane
	clearStatusFile(cfg.StatusFile)
	defer clearStatusFile(cfg.StatusFile)

	backoff := reconnectMinBackoff
	for {
		if ctx.Err() != nil {
			return nil
		}
		a, connErr := connectOnce(ctx, cfg, trust, enrollBase, callbackBase)
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

		a.connectedAt = time.Now().UTC().Format(time.RFC3339)
		a.writeStatus()

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

type StatusSnapshot struct {
	Connected bool          `json:"connected"`
	Proxy     string        `json:"proxy"`
	Since     string        `json:"since"`
	Children  []ChildStatus `json:"children"`
}

// @story: host-agent-control-plane
type ChildStatus struct {
	SpawnID    string `json:"spawn_id"`
	RunScopeID string `json:"run_scope_id"`
	Binding    string `json:"binding"`
}

// @story: host-agent-control-plane
func (a *agent) snapshotChildren() []ChildStatus {
	a.childMu.Lock()
	defer a.childMu.Unlock()
	if len(a.children) == 0 {
		return nil
	}
	out := make([]ChildStatus, 0, len(a.children))
	for _, child := range a.children {
		out = append(out, ChildStatus{
			SpawnID:    child.spawnID,
			RunScopeID: child.runScopeID,
			Binding:    child.bindingPath,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SpawnID < out[j].SpawnID })
	return out
}

// @story: host-agent-control-plane
func (a *agent) writeStatus() {
	writeStatusFile(a.cfg.StatusFile, StatusSnapshot{
		Connected: true,
		Proxy:     a.cfg.RimskyURL,
		Since:     a.connectedAt,
		Children:  a.snapshotChildren(),
	})
}

func writeStatusFile(path string, snap StatusSnapshot) {
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

func connectOnce(ctx context.Context, cfg Config, trust *localTrust, enrollBaseURL, callbackBaseURL string) (*agent, error) {
	creds, err := agentTransportCredentials(cfg)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(cfg.RimskyURL, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("dial proxy: %w", err)
	}

	stream, err := genv1.NewHostAgentClient(conn).Connect(ctx)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("open Connect stream: %w", err)
	}

	a := newAgent(cfg, trust, enrollBaseURL, callbackBaseURL, stream)
	if !a.send(&genv1.ClientFrame{Body: &genv1.ClientFrame_Register{Register: &genv1.Register{
		ApiKey:               cfg.APIKey,
		AgentLabel:           cfg.AgentLabel,
		AgentVersion:         agentVersion,
		LocalCallbackBaseUrl: callbackBaseURL,
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
		a.handleDispatchFrame(ctx, body.DispatchFrame)
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

func bindPlaintextListener(addr string) (net.Listener, string, error) {
	lis, err := net.Listen("tcp", loopbackEnrollAddr(addr))
	if err != nil {
		return nil, "", err
	}
	return lis, "http://" + lis.Addr().String(), nil
}

func loopbackEnrollAddr(addr string) string {
	if addr == "" {
		return "127.0.0.1:0"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return "127.0.0.1:0"
	}
	if host != "" && host != "127.0.0.1" && host != "localhost" && host != "::1" {
		slog.Warn("hostagent: enroll listener is loopback-only; ignoring non-loopback host", "requested_listen", addr, "bound_host", "127.0.0.1")
	}
	return net.JoinHostPort("127.0.0.1", port)
}

func bindCallbackListener() (net.Listener, string, error) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", err
	}
	port := lis.Addr().(*net.TCPAddr).Port
	base := fmt.Sprintf("https://%s:%d", localTrustServerName, port)
	return lis, base, nil
}
