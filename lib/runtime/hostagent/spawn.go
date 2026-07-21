// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: host-agent
package hostagent

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/proto"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/enroll"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const agentPortEnvVar = "RIMSKY_AGENT_PORT"

const errClassSpawnFailed = "spawn_failed"

const portDialInterval = 25 * time.Millisecond

const maxSpawnPortAttempts = 3

var errChildDidNotBind = errors.New("child did not bind picked port")

func (a *agent) runSpawn(ctx context.Context, sp *genv1.Spawn) {
	ack := a.handleSpawn(ctx, sp)
	a.send(&genv1.ClientFrame{Body: &genv1.ClientFrame_SpawnAck{SpawnAck: ack}})
}

type SpawnServiceParams struct {
	BinaryPath   string
	Args         []string
	Cwd          string
	Env          []string
	DialTLS      *tls.Config
	ReadyTimeout time.Duration
	portSource   func() (int, error)
}

type SpawnedService struct {
	Cmd    *exec.Cmd
	Port   int
	Exited <-chan struct{}
}

func SpawnService(ctx context.Context, params SpawnServiceParams) (*SpawnedService, error) {
	pickPort := params.portSource
	if pickPort == nil {
		pickPort = FreeLocalPort
	}

	var lastErr error
	for attempt := 0; attempt < maxSpawnPortAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		spawned, err := spawnOnPickedPort(ctx, params, pickPort)
		if err == nil {
			return spawned, nil
		}
		lastErr = err
		if !errors.Is(err, errChildDidNotBind) {
			return nil, err
		}
	}
	return nil, lastErr
}

func spawnOnPickedPort(ctx context.Context, params SpawnServiceParams, pickPort func() (int, error)) (*SpawnedService, error) {
	port, err := pickPort()
	if err != nil {
		return nil, fmt.Errorf("allocate port: %w", err)
	}

	cmd := exec.Command(params.BinaryPath, params.Args...) //nolint:gosec
	if params.Cwd != "" {
		cmd.Dir = params.Cwd
	}

	env := setEnvVar(params.Env, agentPortEnvVar, strconv.Itoa(port))
	cmd.Env = env
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("exec start: %w", err)
	}

	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()

	readyTimeout := params.ReadyTimeout
	if readyTimeout <= 0 {
		readyTimeout = 30 * time.Second
	}

	switch waitPortReady(ctx, port, exited, readyTimeout, params.DialTLS) {
	case portReady:
		return &SpawnedService{Cmd: cmd, Port: port, Exited: exited}, nil
	case portWaitCtxDone:
		killProcess(cmd)
		<-exited
		return nil, ctx.Err()
	case portWaitChildExited:
		killProcess(cmd)
		<-exited
		return nil, fmt.Errorf("child exited before binding port %d: %w", port, errChildDidNotBind)
	default:
		killProcess(cmd)
		<-exited
		return nil, fmt.Errorf("child did not bind port %d within %s: %w", port, readyTimeout, errChildDidNotBind)
	}
}

func (a *agent) handleSpawn(ctx context.Context, sp *genv1.Spawn) *genv1.SpawnAck {
	path := sp.GetBinding().GetPath()
	if path == "" {
		return spawnFailed(sp.GetSpawnId(), "binding.path is empty")
	}
	execPath, err := a.resolveBindingPath(path)
	if err != nil {
		return spawnFailed(sp.GetSpawnId(), err.Error())
	}

	// @story: host-agent-per-binding-overrides
	cwd := sp.GetBinding().GetCwd()
	if cwd == "" {
		cwd = sp.GetCwd()
	}

	// @story: host-agent-per-binding-overrides
	readyTimeoutSeconds := sp.GetBinding().GetReadyTimeoutSeconds()
	if readyTimeoutSeconds == 0 {
		readyTimeoutSeconds = sp.GetReadyTimeoutSeconds()
	}

	env := os.Environ()
	for k, v := range sp.GetBinding().GetEnv() {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}

	spawnID := sp.GetSpawnId()
	token, err := generateSpawnSecret()
	if err != nil {
		return spawnFailed(spawnID, err.Error())
	}
	a.registerBootstrapToken(spawnID, token, time.Now())

	env = setEnvVar(env, enroll.EnvPeerAuth, enroll.PeerAuthMTLS)
	env = setEnvVar(env, enroll.EnvAPIKey, token)
	env = setEnvVar(env, enroll.EnvControlAPIURL, a.enrollBaseURL)

	spawned, err := SpawnService(ctx, SpawnServiceParams{
		BinaryPath:   execPath,
		Args:         sp.GetBinding().GetArgs(),
		Cwd:          cwd,
		Env:          env,
		DialTLS:      a.trust.dialChildTLSConfig(),
		ReadyTimeout: time.Duration(readyTimeoutSeconds) * time.Second,
	})
	if err != nil {
		a.forgetBootstrapToken(spawnID)
		return spawnFailed(spawnID, err.Error())
	}

	conn, err := a.dialChild(spawned.Port)
	if err != nil {
		killProcess(spawned.Cmd)
		<-spawned.Exited
		a.forgetBootstrapToken(spawnID)
		return spawnFailed(spawnID, "dial child: "+err.Error())
	}

	caps, err := handshakeCapabilities(ctx, conn, sp.GetExpectedProtocols())
	if err != nil {
		_ = conn.Close()
		killProcess(spawned.Cmd)
		<-spawned.Exited
		a.forgetBootstrapToken(spawnID)
		return spawnFailed(spawnID, err.Error())
	}

	a.childMu.Lock()
	prior, hadPrior := a.children[spawnID]
	a.children[spawnID] = &liveChild{
		spawnID:     spawnID,
		runScopeID:  sp.GetRunScopeId(),
		bindingPath: path,
		cmd:         spawned.Cmd,
		conn:        conn,
		port:        spawned.Port,
		exited:      spawned.Exited,
	}
	a.childMu.Unlock()
	if hadPrior {
		slog.Warn("hostagent: spawn_id collided with a still-live child; terminating the prior process before replacing it",
			"spawn_id", spawnID)
		grace := a.cfg.ReapGracePeriod
		go func() {
			terminateChild(prior, grace)
			if prior.conn != nil {
				_ = prior.conn.Close()
			}
		}()
	}
	a.writeStatus()

	slog.Info("hostagent: spawned child", "spawn_id", sp.GetSpawnId(), "path", execPath, "port", spawned.Port, "protocols", sp.GetExpectedProtocols())
	return &genv1.SpawnAck{
		SpawnId:      sp.GetSpawnId(),
		Status:       genv1.SpawnAck_SPAWN_STATUS_READY,
		Capabilities: caps,
	}
}

func (a *agent) dialChild(port int) (*grpc.ClientConn, error) {
	creds := credentials.NewTLS(a.trust.dialChildTLSConfig())
	return grpc.NewClient(fmt.Sprintf("127.0.0.1:%d", port), grpc.WithTransportCredentials(creds))
}

func setEnvVar(env []string, key, val string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			continue
		}
		out = append(out, e)
	}
	return append(out, key+"="+val)
}

func (a *agent) resolveBindingPath(path string) (string, error) {
	resolved, rerr := canonicalPath(path)
	if len(a.cfg.AllowPaths) == 0 {
		if rerr != nil {
			return path, nil
		}
		return resolved, nil
	}
	if rerr != nil {
		return "", fmt.Errorf("path %q could not be resolved for --allow-paths matching: %w", path, rerr)
	}
	for _, glob := range a.cfg.AllowPaths {
		if glob == "" {
			continue
		}
		pattern, ok := canonicalGlob(glob)
		if !ok {
			continue
		}
		if matched, merr := filepath.Match(pattern, resolved); merr == nil && matched {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("path %q not permitted by --allow-paths", path)
}

func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(filepath.Clean(abs))
}

func canonicalGlob(glob string) (string, bool) {
	base, rest := splitGlobBase(glob)
	if base == "" {
		base = "."
	}
	resolvedBase, err := canonicalPath(base)
	if err != nil {
		return "", false
	}
	if rest == "" {
		return resolvedBase, true
	}
	return filepath.Join(resolvedBase, rest), true
}

func splitGlobBase(glob string) (base, rest string) {
	sep := string(filepath.Separator)
	parts := strings.Split(glob, sep)
	magicIdx := len(parts)
	for i, part := range parts {
		if strings.ContainsAny(part, `*?[\`) {
			magicIdx = i
			break
		}
	}
	return strings.Join(parts[:magicIdx], sep), strings.Join(parts[magicIdx:], sep)
}

func handshakeCapabilities(ctx context.Context, conn *grpc.ClientConn, protocols []string) (map[string][]byte, error) {
	caps := map[string][]byte{}
	for _, protocol := range protocols {
		raw, err := capabilitiesForProtocol(ctx, conn, protocol)
		if err != nil {
			return nil, fmt.Errorf("capabilities handshake for %q: %w", protocol, err)
		}
		caps[protocol] = raw
	}
	return caps, nil
}

func capabilitiesForProtocol(ctx context.Context, conn *grpc.ClientConn, protocol string) ([]byte, error) {
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	switch protocol {
	case protocolExecutor:
		resp, err := genv1.NewExecutorObservabilityClient(conn).Capabilities(callCtx, &genv1.ExecutorCapabilitiesRequest{})
		if err != nil {
			return nil, err
		}
		return proto.Marshal(resp)
	case protocolClaimProducer:
		resp, err := genv1.NewClaimProducerClient(conn).Capabilities(callCtx, &genv1.CapabilitiesRequest{})
		if err != nil {
			return nil, err
		}
		return proto.Marshal(resp)
	default:
		return nil, fmt.Errorf("unsupported protocol %q", protocol)
	}
}

func (a *agent) runReap(reap *genv1.Reap) {
	a.send(&genv1.ClientFrame{Body: &genv1.ClientFrame_Reaped{Reaped: a.handleReap(reap)}})
}

func (a *agent) handleReap(reap *genv1.Reap) *genv1.Reaped {
	a.childMu.Lock()
	child, ok := a.children[reap.GetSpawnId()]
	if ok {
		delete(a.children, reap.GetSpawnId())
	}
	a.childMu.Unlock()
	if ok {
		a.writeStatus()
	}
	a.forgetBootstrapToken(reap.GetSpawnId())

	if !ok {
		return &genv1.Reaped{SpawnId: reap.GetSpawnId(), Clean: true}
	}

	grace := time.Duration(reap.GetSigtermGraceSeconds()) * time.Second
	if grace <= 0 {
		grace = a.cfg.ReapGracePeriod
	}

	clean := terminateChild(child, grace)
	if child.conn != nil {
		_ = child.conn.Close()
	}
	slog.Info("hostagent: reaped child", "spawn_id", reap.GetSpawnId(), "clean", clean)
	return &genv1.Reaped{SpawnId: reap.GetSpawnId(), Clean: clean}
}

func terminateChild(child *liveChild, grace time.Duration) bool {
	if child.cmd.Process != nil {
		_ = child.cmd.Process.Signal(syscall.SIGTERM)
	}
	select {
	case <-child.exited:
		return cleanExit(child.cmd)
	case <-time.After(grace):
		killProcess(child.cmd)
		<-child.exited
		return false
	}
}

func (a *agent) reapAllOrphans(grace time.Duration) {
	a.childMu.Lock()
	orphans := make([]*liveChild, 0, len(a.children))
	for id := range a.children {
		orphans = append(orphans, a.children[id])
		delete(a.children, id)
	}
	a.childMu.Unlock()

	if len(orphans) == 0 {
		return
	}
	slog.Info("hostagent: reaping orphaned children after stream close", "count", len(orphans), "grace", grace)
	for _, child := range orphans {
		terminateChild(child, grace)
		if child.conn != nil {
			_ = child.conn.Close()
		}
	}
}

func cleanExit(cmd *exec.Cmd) bool {
	if cmd.ProcessState == nil {
		return false
	}
	return cmd.ProcessState.ExitCode() == 0
}

func killProcess(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func spawnFailed(spawnID, msg string) *genv1.SpawnAck {
	return &genv1.SpawnAck{
		SpawnId: spawnID,
		Status:  genv1.SpawnAck_SPAWN_STATUS_FAILED,
		Error:   &genv1.HostAgentError{Class: errClassSpawnFailed, Message: msg},
	}
}

func generateSpawnSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("hostagent: generate spawn secret: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func FreeLocalPort() (int, error) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := lis.Addr().(*net.TCPAddr).Port
	_ = lis.Close()
	return port, nil
}

type portWaitOutcome int

const (
	portReady portWaitOutcome = iota
	portWaitCtxDone
	portWaitChildExited
	portWaitTimedOut
)

func waitPortReady(ctx context.Context, port int, exited <-chan struct{}, timeout time.Duration, dialTLS *tls.Config) portWaitOutcome {
	deadline := time.Now().Add(timeout)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for {
		select {
		case <-ctx.Done():
			return portWaitCtxDone
		case <-exited:
			return portWaitChildExited
		default:
		}
		if probeReady(addr, dialTLS) {
			return portReady
		}
		if time.Now().After(deadline) {
			return portWaitTimedOut
		}
		time.Sleep(portDialInterval)
	}
}

func probeReady(addr string, dialTLS *tls.Config) bool {
	dialer := &net.Dialer{Timeout: portDialInterval}
	if dialTLS == nil {
		conn, err := dialer.Dial("tcp", addr)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}
	conn, err := tls.DialWithDialer(dialer, "tcp", addr, dialTLS)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
