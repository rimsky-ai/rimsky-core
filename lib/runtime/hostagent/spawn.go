// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: host-agent
package hostagent

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const agentPortEnvVar = "RIMSKY_AGENT_PORT"

const errClassSpawnFailed = "spawn_failed"

const portDialInterval = 25 * time.Millisecond

func (a *agent) runSpawn(ctx context.Context, sp *genv1.Spawn) {
	ack := a.handleSpawn(ctx, sp)
	a.send(&genv1.ClientFrame{Body: &genv1.ClientFrame_SpawnAck{SpawnAck: ack}})
}

type SpawnServiceParams struct {
	BinaryPath string
	Args []string
	Cwd string
	Env []string
	ReadyTimeout time.Duration
}

type SpawnedService struct {
	Cmd *exec.Cmd
	Port   int
	Exited <-chan struct{}
}

// @blessed-invariant: spawn-no-leak-on-readiness-timeout
func SpawnService(ctx context.Context, params SpawnServiceParams) (*SpawnedService, error) {
	port, err := FreeLocalPort()
	if err != nil {
		return nil, fmt.Errorf("allocate port: %w", err)
	}

	cmd := exec.Command(params.BinaryPath, params.Args...) //nolint:gosec // path trust is the caller's posture (host-agent enforces allow-paths via pathAllowed; future callers MUST validate the binary path against their own trust model before invoking SpawnService)
	if params.Cwd != "" {
		cmd.Dir = params.Cwd
	}

	env := append([]string(nil), params.Env...)
	env = append(env, fmt.Sprintf("%s=%d", agentPortEnvVar, port))
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

	if !waitPortReady(ctx, port, exited, readyTimeout) {
		// @blessed-invariant: spawn-no-leak-on-readiness-timeout
		killProcess(cmd)
		<-exited
		return nil, fmt.Errorf("child did not bind port %d within %s", port, readyTimeout)
	}

	return &SpawnedService{Cmd: cmd, Port: port, Exited: exited}, nil
}

func (a *agent) handleSpawn(ctx context.Context, sp *genv1.Spawn) *genv1.SpawnAck {
	path := sp.GetBinding().GetPath()
	if path == "" {
		return spawnFailed(sp.GetSpawnId(), "binding.path is empty")
	}
	if !a.pathAllowed(path) {
		return spawnFailed(sp.GetSpawnId(), fmt.Sprintf("path %q not permitted by --allow-paths", path))
	}

	// @story: host-agent-per-binding-overrides — argv, working directory,
	// and env are carried on the Binding wire message. A binding that declares
	// none of them spawns exactly as before (no extra args, the instance-level
	// cwd, inherited env), so this stays backward compatible.
	//
	// @deliberate: per-binding cwd overrides the instance-level cwd only when
	// set; otherwise fall back to the Spawn frame's instance-level cwd
	// (today's behavior).
	cwd := sp.GetBinding().GetCwd()
	if cwd == "" {
		cwd = sp.GetCwd()
	}

	env := os.Environ()
	for k, v := range sp.GetBinding().GetEnv() {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}

	spawned, err := SpawnService(ctx, SpawnServiceParams{
		BinaryPath:   path,
		Args:         sp.GetBinding().GetArgs(),
		Cwd:          cwd,
		Env:          env,
		ReadyTimeout: time.Duration(sp.GetReadyTimeoutSeconds()) * time.Second,
	})
	if err != nil {
		return spawnFailed(sp.GetSpawnId(), err.Error())
	}

	conn, err := grpc.NewClient(fmt.Sprintf("127.0.0.1:%d", spawned.Port), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		killProcess(spawned.Cmd)
		<-spawned.Exited
		return spawnFailed(sp.GetSpawnId(), "dial child: "+err.Error())
	}

	caps, err := handshakeCapabilities(ctx, conn, sp.GetExpectedProtocols())
	if err != nil {
		_ = conn.Close()
		killProcess(spawned.Cmd)
		<-spawned.Exited
		return spawnFailed(sp.GetSpawnId(), err.Error())
	}

	a.childMu.Lock()
	a.children[sp.GetSpawnId()] = &liveChild{
		spawnID: sp.GetSpawnId(),
		cmd:     spawned.Cmd,
		conn:    conn,
		port:    spawned.Port,
		exited:  spawned.Exited,
	}
	a.childMu.Unlock()

	slog.Info("hostagent: spawned child", "spawn_id", sp.GetSpawnId(), "path", path, "port", spawned.Port, "protocols", sp.GetExpectedProtocols())
	return &genv1.SpawnAck{
		SpawnId:      sp.GetSpawnId(),
		Status:       genv1.SpawnAck_SPAWN_STATUS_READY,
		Capabilities: caps,
	}
}

func (a *agent) pathAllowed(path string) bool {
	if len(a.cfg.AllowPaths) == 0 {
		return true
	}
	for _, glob := range a.cfg.AllowPaths {
		if glob == "" {
			continue
		}
		if ok, err := filepath.Match(glob, path); err == nil && ok {
			return true
		}
	}
	return false
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
	case protocolPublisher:
		resp, err := genv1.NewPublisherClient(conn).Capabilities(callCtx, &emptypb.Empty{})
		if err != nil {
			return nil, err
		}
		return proto.Marshal(resp)
	case protocolDataProcessing:
		resp, err := genv1.NewDataProcessingClient(conn).Capabilities(callCtx, &emptypb.Empty{})
		if err != nil {
			return nil, err
		}
		return proto.Marshal(resp)
	case protocolValidation:
		return probeValidationRegistered(callCtx, conn)
	default:
		return nil, fmt.Errorf("unsupported protocol %q", protocol)
	}
}

const validationHandshakeProbeRole = ""

func probeValidationRegistered(ctx context.Context, conn *grpc.ClientConn) ([]byte, error) {
	_, err := genv1.NewValidationClient(conn).Validate(ctx, &genv1.ValidateRequest{Role: validationHandshakeProbeRole})
	if err == nil {
		return nil, nil
	}
	if status.Code(err) == codes.Unimplemented {
		return nil, fmt.Errorf("validation service not registered on spawned binary: %w", err)
	}
	return nil, nil
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

func FreeLocalPort() (int, error) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := lis.Addr().(*net.TCPAddr).Port
	_ = lis.Close()
	return port, nil
}

func waitPortReady(ctx context.Context, port int, exited <-chan struct{}, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for {
		select {
		case <-ctx.Done():
			return false
		case <-exited:
			return false
		default:
		}
		conn, err := net.DialTimeout("tcp", addr, portDialInterval)
		if err == nil {
			_ = conn.Close()
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(portDialInterval)
	}
}
