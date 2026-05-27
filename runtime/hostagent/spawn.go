// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// spawn.go — Spawn and Reap handling. On Spawn the agent validates the
// binding path against the allow-list, picks a free local port, exec()s the
// binary with RIMSKY_AGENT_PORT in its environment (the v1 contract for how a
// spawned binding learns which port to bind), poll-dials that port until the
// child's gRPC server is up, runs the Capabilities handshake for each expected
// protocol, registers the live child, and replies SpawnAck. On Reap it
// SIGTERMs the child, SIGKILLs after the grace period, waits, and replies
// Reaped.
//
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
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"

	genv1 "github.com/rimsky-ai/rimsky-core/protocols/proto/v1/gen"
)

// agentPortEnvVar is how the agent tells a spawned binding which localhost
// port to bind its gRPC server on. The agent allocates the port; the child
// does not communicate any port back. Plan-invented v1 contract: spawned
// rimsky-service binaries MUST read RIMSKY_AGENT_PORT and bind there.
const agentPortEnvVar = "RIMSKY_AGENT_PORT"

// Error classes the agent surfaces on a failed spawn (slot into the proxy's
// existing host-agent error-class vocabulary).
const (
	errClassSpawnFailed = "spawn_failed"
)

// portDialInterval is the poll cadence while waiting for the child's gRPC
// port to come up.
const portDialInterval = 25 * time.Millisecond

// runSpawn handles a Spawn frame and sends the resulting SpawnAck.
func (a *agent) runSpawn(ctx context.Context, sp *genv1.Spawn) {
	ack := a.handleSpawn(ctx, sp)
	a.send(&genv1.ClientFrame{Body: &genv1.ClientFrame_SpawnAck{SpawnAck: ack}})
}

// handleSpawn validates, exec()s, handshakes, and registers a spawned child.
func (a *agent) handleSpawn(ctx context.Context, sp *genv1.Spawn) *genv1.SpawnAck {
	path := sp.GetBinding().GetPath()
	if path == "" {
		return spawnFailed(sp.GetSpawnId(), "binding.path is empty")
	}
	if !a.pathAllowed(path) {
		return spawnFailed(sp.GetSpawnId(), fmt.Sprintf("path %q not permitted by --allow-paths", path))
	}

	port, err := freeLocalPort()
	if err != nil {
		return spawnFailed(sp.GetSpawnId(), "allocate port: "+err.Error())
	}

	cmd := exec.Command(path) //nolint:gosec // path trust is the agent's documented posture (allow-paths is the knob)
	if cwd := sp.GetCwd(); cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Env = append(os.Environ(), fmt.Sprintf("%s=%d", agentPortEnvVar, port))
	cmd.Stdout = os.Stderr // surface child logs without polluting the agent's stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return spawnFailed(sp.GetSpawnId(), "exec start: "+err.Error())
	}

	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()

	readyTimeout := time.Duration(sp.GetReadyTimeoutSeconds()) * time.Second
	if readyTimeout <= 0 {
		readyTimeout = 30 * time.Second
	}

	if !waitPortReady(ctx, port, exited, readyTimeout) {
		killProcess(cmd)
		<-exited
		return spawnFailed(sp.GetSpawnId(), fmt.Sprintf("child did not bind port %d within %s", port, readyTimeout))
	}

	conn, err := grpc.NewClient(fmt.Sprintf("127.0.0.1:%d", port), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		killProcess(cmd)
		<-exited
		return spawnFailed(sp.GetSpawnId(), "dial child: "+err.Error())
	}

	caps, err := handshakeCapabilities(ctx, conn, sp.GetExpectedProtocols())
	if err != nil {
		_ = conn.Close()
		killProcess(cmd)
		<-exited
		return spawnFailed(sp.GetSpawnId(), err.Error())
	}

	a.childMu.Lock()
	a.children[sp.GetSpawnId()] = &liveChild{
		spawnID: sp.GetSpawnId(),
		cmd:     cmd,
		conn:    conn,
		port:    port,
		exited:  exited,
	}
	a.childMu.Unlock()

	slog.Info("hostagent: spawned child", "spawn_id", sp.GetSpawnId(), "path", path, "port", port, "protocols", sp.GetExpectedProtocols())
	return &genv1.SpawnAck{
		SpawnId:      sp.GetSpawnId(),
		Status:       genv1.SpawnAck_SPAWN_STATUS_READY,
		Capabilities: caps,
	}
}

// pathAllowed reports whether path satisfies the allow-list. An empty
// allow-list is permissive (the default trust posture).
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

// handshakeCapabilities dials each expected protocol's Capabilities RPC on the
// child and returns the proto.Marshal'd responses keyed by protocol name.
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

// capabilitiesForProtocol calls the named protocol's Capabilities RPC and
// returns the serialized response.
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

// runReap handles a Reap frame and sends the resulting Reaped.
func (a *agent) runReap(reap *genv1.Reap) {
	a.send(&genv1.ClientFrame{Body: &genv1.ClientFrame_Reaped{Reaped: a.handleReap(reap)}})
}

// handleReap SIGTERMs the child, SIGKILLs after the grace period, waits, and
// reports whether the child exited cleanly.
func (a *agent) handleReap(reap *genv1.Reap) *genv1.Reaped {
	a.childMu.Lock()
	child, ok := a.children[reap.GetSpawnId()]
	if ok {
		delete(a.children, reap.GetSpawnId())
	}
	a.childMu.Unlock()

	if !ok {
		// Unknown spawn-id: nothing to reap. Report clean (idempotent reap).
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

// terminateChild SIGTERMs the child, SIGKILLs after grace, waits, and returns
// whether the child exited cleanly (exit code 0).
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

// reapAllOrphans is called after a stream close: every still-live child is an
// orphan (its spawn-id is dead on reconnect). Give them the grace period to
// exit, then SIGKILL stragglers. Drains the children map.
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

// cleanExit reports whether the finished command exited with code 0.
func cleanExit(cmd *exec.Cmd) bool {
	if cmd.ProcessState == nil {
		return false
	}
	return cmd.ProcessState.ExitCode() == 0
}

// killProcess SIGKILLs the child if it has a live process handle.
func killProcess(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// spawnFailed builds a FAILED SpawnAck carrying the error class + message.
func spawnFailed(spawnID, msg string) *genv1.SpawnAck {
	return &genv1.SpawnAck{
		SpawnId: spawnID,
		Status:  genv1.SpawnAck_SPAWN_STATUS_FAILED,
		Error:   &genv1.HostAgentError{Class: errClassSpawnFailed, Message: msg},
	}
}

// freeLocalPort opens a listener on an OS-assigned port, reads it back, and
// closes immediately. The brief race window (another process grabbing the
// port before the child binds) is accepted per the spec's spawn contract.
func freeLocalPort() (int, error) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := lis.Addr().(*net.TCPAddr).Port
	_ = lis.Close()
	return port, nil
}

// waitPortReady poll-dials 127.0.0.1:port until a TCP connection succeeds, the
// child exits, ctx is cancelled, or the timeout elapses.
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
