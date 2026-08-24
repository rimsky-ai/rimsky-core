// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: host-daemon
package hostdaemon

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
	"google.golang.org/grpc/keepalive"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/sillyname"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const daemonVersion = "v1"

const reconnectMinBackoff = 250 * time.Millisecond

const reconnectMaxBackoff = 10 * time.Second

var proxyKeepaliveParams = keepalive.ClientParameters{
	Time:                10 * time.Second,
	Timeout:             5 * time.Second,
	PermitWithoutStream: true,
}

type liveChild struct {
	spawnID     string
	runScopeID  string
	bindingPath string
	cmd         *exec.Cmd
	conn        *grpc.ClientConn
	port        int
	exited      <-chan struct{}
}

type daemon struct {
	cfg             Config
	trust           *localTrust
	enrollBaseURL   string
	callbackBaseURL string
	connectedAt     string

	sendMu     sync.Mutex
	stream     genv1.HostDaemon_ConnectClient
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

func newDaemon(cfg Config, trust *localTrust, enrollBaseURL, callbackBaseURL string, stream genv1.HostDaemon_ConnectClient) *daemon {
	return &daemon{
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

func (a *daemon) send(frame *genv1.ClientFrame) bool {
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

func (a *daemon) markSendClosed() {
	a.sendMu.Lock()
	a.sendClosed = true
	a.sendMu.Unlock()
}

func Run(ctx context.Context, cfg Config) error {
	cfg = cfg.withDefaults()
	if cfg.ProxyURL == "" {
		return errors.New("hostdaemon: RIMSKY_HOST_DAEMON_PROXY_URL is required")
	}
	anonymous := cfg.APIKey == ""
	if anonymous {
		cfg.APIKey = sillyname.AnonymousCredentialSentinel
		if cfg.IdentityFile == "" {
			path, ferr := DefaultIdentityFile()
			if ferr != nil {
				return ferr
			}
			cfg.IdentityFile = path
		}
		if cfg.RoutingLabel == "" {
			id, lerr := LoadIdentity(cfg.IdentityFile)
			if lerr != nil {
				return lerr
			}
			cfg.RoutingLabel = id.RoutingIdentity
		}
	}

	trust, err := newLocalTrust(time.Now())
	if err != nil {
		return fmt.Errorf("hostdaemon: %w", err)
	}

	var (
		curMu sync.Mutex
		cur   *daemon
	)
	currentDaemon := func() *daemon {
		curMu.Lock()
		defer curMu.Unlock()
		return cur
	}

	enrollLis, enrollBase, err := bindPlaintextListener(cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("hostdaemon: bind enroll listener: %w", err)
	}
	defer enrollLis.Close()

	callbackLis, callbackBase, err := bindCallbackListener()
	if err != nil {
		return fmt.Errorf("hostdaemon: bind callback listener: %w", err)
	}
	defer callbackLis.Close()

	enrollSrv := &http.Server{Handler: localEnrollHandler(trust, currentDaemon)}
	callbackSrv := &http.Server{
		Handler:   localForwardHandler(currentDaemon),
		TLSConfig: trust.callbackServerTLSConfig(),
	}
	go func() {
		if serveErr := enrollSrv.Serve(enrollLis); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			slog.Error("HOSTDAEMON.ENROLLLISTENER.SERVESTOPPED", "error", serveErr)
		}
	}()
	go func() {
		if serveErr := callbackSrv.ServeTLS(callbackLis, "", ""); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			slog.Error("HOSTDAEMON.CALLBACKLISTENER.SERVESTOPPED", "error", serveErr)
		}
	}()
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = enrollSrv.Shutdown(shutCtx)
		_ = callbackSrv.Shutdown(shutCtx)
	}()

	slog.Info("HOSTDAEMON.PROCESS.STARTING", "proxy_url", cfg.ProxyURL, "daemon_label", cfg.DaemonLabel, "enroll_base_url", enrollBase, "callback_base_url", callbackBase)

	// @story: host-daemon-control-plane
	clearStatusFile(cfg.StatusFile)
	defer clearStatusFile(cfg.StatusFile)

	backoff := reconnectMinBackoff
	var gate reconnectLogGate
	for {
		if ctx.Err() != nil {
			return nil
		}
		a, connErr := connectOnce(ctx, cfg, trust, enrollBase, callbackBase)
		if connErr != nil {
			if ctx.Err() != nil {
				return nil
			}
			gate.report(connErr, backoff)
			if !sleepCtx(ctx, backoff) {
				return nil
			}
			backoff = nextBackoff(backoff)
			continue
		}
		backoff = reconnectMinBackoff
		gate.reset()

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
		slog.Info("HOSTDAEMON.STREAM.RECONNECTING", "detail", "the stream closed", "backoff", backoff)
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

// @story: host-daemon-control-plane
type ChildStatus struct {
	SpawnID    string `json:"spawn_id"`
	RunScopeID string `json:"run_scope_id"`
	Binding    string `json:"binding"`
}

// @story: host-daemon-control-plane
func (a *daemon) snapshotChildren() []ChildStatus {
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

// @story: host-daemon-control-plane
func (a *daemon) writeStatus() {
	writeStatusFile(a.cfg.StatusFile, StatusSnapshot{
		Connected: true,
		Proxy:     a.cfg.ProxyURL,
		Since:     a.connectedAt,
		Children:  a.snapshotChildren(),
	})
}

func writeStatusFile(path string, snap StatusSnapshot) {
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		slog.Warn("HOSTDAEMON.STATUSFILE.MKDIRFAILED", "path", path, "error", err)
		return
	}
	body, err := json.Marshal(snap)
	if err != nil {
		slog.Warn("HOSTDAEMON.STATUSFILE.MARSHALFAILED", "error", err)
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		slog.Warn("HOSTDAEMON.STATUSFILE.WRITEFAILED", "path", path, "error", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		slog.Warn("HOSTDAEMON.STATUSFILE.RENAMEFAILED", "path", path, "error", err)
	}
}

func clearStatusFile(path string) {
	if path == "" {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		slog.Warn("HOSTDAEMON.STATUSFILE.REMOVEFAILED", "path", path, "error", err)
	}
}

// @concept: host-daemon
type reconnectLogGate struct {
	lastError string
	silenced  bool
}

func (g *reconnectLogGate) report(connErr error, backoff time.Duration) {
	emit, last := g.decide(connErr.Error(), backoff)
	if !emit {
		return
	}
	kv := []any{"error", connErr, "backoff", backoff}
	if last {
		kv = append(kv, "repeat_logging",
			"the daemon stays quiet while this error repeats at the maximum backoff. The next line names a changed error.")
	}
	slog.Warn("HOSTDAEMON.PROXYCONNECT.FAILED", kv...)
}

func (g *reconnectLogGate) decide(msg string, backoff time.Duration) (emit, last bool) {
	if msg != g.lastError {
		g.lastError = msg
		g.silenced = false
		return true, false
	}
	if backoff < reconnectMaxBackoff {
		return true, false
	}
	if g.silenced {
		return false, false
	}
	g.silenced = true
	return true, true
}

func (g *reconnectLogGate) reset() {
	g.lastError = ""
	g.silenced = false
}

func connectOnce(ctx context.Context, cfg Config, trust *localTrust, enrollBaseURL, callbackBaseURL string) (*daemon, error) {
	creds, err := daemonTransportCredentials(cfg)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(cfg.ProxyURL,
		grpc.WithTransportCredentials(creds),
		grpc.WithKeepaliveParams(proxyKeepaliveParams),
	)
	if err != nil {
		return nil, fmt.Errorf("dial proxy: %w", err)
	}

	stream, err := genv1.NewHostDaemonClient(conn).Connect(ctx)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("open Connect stream: %w", err)
	}

	a := newDaemon(cfg, trust, enrollBaseURL, callbackBaseURL, stream)
	if !a.send(&genv1.ClientFrame{Body: &genv1.ClientFrame_Register{Register: &genv1.Register{
		ApiKey:               cfg.APIKey,
		DaemonLabel:          cfg.DaemonLabel,
		DaemonVersion:        daemonVersion,
		LocalCallbackBaseUrl: callbackBaseURL,
		RoutingLabel:         cfg.RoutingLabel,
	}}}) {
		_ = conn.Close()
		return nil, errors.New("send Register failed")
	}

	first, err := recvWithTimeout(stream, cfg.RegisterAckTimeout)
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
		slog.Warn("HOSTDAEMON.PRIORCONNECTION.DISPLACED")
	}
	if cfg.APIKey == sillyname.AnonymousCredentialSentinel && ack.GetRoutingIdentity() != "" && cfg.IdentityFile != "" {
		if err := SaveIdentity(cfg.IdentityFile, PersistedIdentity{RoutingIdentity: ack.GetRoutingIdentity()}); err != nil {
			slog.Warn("HOSTDAEMON.IDENTITYFILE.WRITEFAILED", "path", cfg.IdentityFile, "error", err)
		}
	}
	a.proxyConn = conn
	slog.Info("HOSTDAEMON.PROXYCONNECT.SUCCEEDED", "proxy_version", ack.GetProxyVersion(), "routing_identity", ack.GetRoutingIdentity())
	return a, nil
}

func recvWithTimeout(stream genv1.HostDaemon_ConnectClient, timeout time.Duration) (*genv1.ServerFrame, error) {
	type result struct {
		frame *genv1.ServerFrame
		err   error
	}
	resultCh := make(chan result, 1)
	go func() {
		frame, err := stream.Recv()
		resultCh <- result{frame, err}
	}()
	select {
	case res := <-resultCh:
		return res.frame, res.err
	case <-time.After(timeout):
		return nil, fmt.Errorf("timed out after %s", timeout)
	}
}

func (a *daemon) serve(ctx context.Context) {
	connCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go a.heartbeatLoop(connCtx)

	for {
		frame, err := a.stream.Recv()
		if err != nil {
			if !errors.Is(err, io.EOF) && ctx.Err() == nil {
				slog.Warn("HOSTDAEMON.STREAM.RECVENDED", "error", err)
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

func (a *daemon) routeServerFrame(ctx context.Context, frame *genv1.ServerFrame) {
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
		slog.Warn("HOSTDAEMON.SERVERFRAME.UNKNOWN", "type", fmt.Sprintf("%T", frame.GetBody()))
	}
}

func (a *daemon) heartbeatLoop(ctx context.Context) {
	t := time.NewTicker(a.cfg.HeartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if !a.send(&genv1.ClientFrame{Body: &genv1.ClientFrame_Heartbeat{Heartbeat: &genv1.HostDaemonHeartbeat{
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
		slog.Warn("HOSTDAEMON.ENROLLLISTENER.LOOPBACKONLY", "detail", "ignoring the non-loopback host the configuration asked for", "requested_listen", addr, "bound_host", "127.0.0.1")
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
