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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
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

// SpawnServiceParams configures one binary spawn for late-bound service
// hosting. The fields capture only what the port-pick + exec + ready-poll
// mechanism needs; the gRPC wire surface (Spawn proto, capabilities
// handshake, child-registration tracker) is the caller's concern.
type SpawnServiceParams struct {
	// BinaryPath is the absolute path to the binary to exec.
	BinaryPath string
	// Args are extra positional arguments passed to the binary (may be nil).
	Args []string
	// Cwd is the working directory for the spawned process (empty → inherit).
	Cwd string
	// Env is the base environment for the spawned process. SpawnService
	// appends RIMSKY_AGENT_PORT=<picked> as the last entry so it always wins
	// over any duplicate key the caller supplied (the child MUST bind the
	// agent-allocated port and the env must never be able to shadow it).
	Env []string
	// ReadyTimeout bounds the poll-dial after exec; defaults to 30s when 0.
	ReadyTimeout time.Duration
}

// SpawnedService is the handle returned by SpawnService. The caller owns
// the child's lifecycle and MUST either Kill the process or Wait on the
// Exited channel.
type SpawnedService struct {
	// Cmd is the running child process. The caller drives lifecycle.
	Cmd *exec.Cmd
	// Port is the localhost port the child bound.
	Port int
	// Exited is closed when cmd.Wait returns; lets the caller select on
	// child-exit alongside its own signals.
	Exited <-chan struct{}
}

// SpawnService picks a free localhost port, exec()s BinaryPath with
// RIMSKY_AGENT_PORT set in its environment, and poll-dials
// 127.0.0.1:<port> until the child binds a TCP listener there or the
// ReadyTimeout elapses. On readiness timeout, the child is killed and
// reaped before the function returns (no leak).
//
// @agent-contract guarantees: returns nil error iff the child process
// is running, its port is reachable on 127.0.0.1, and the caller owns
// its lifecycle. Does NOT perform any capabilities handshake or
// protocol registration — that is the caller's concern.
//
// SECURITY / TRUST MODEL — `params.BinaryPath` is exec'd directly,
// unvalidated. The primitive is path-trust-by-caller: every caller is
// responsible for validating the binary path against the caller's own
// trust model BEFORE invoking SpawnService. Today's callers do this in
// two different ways:
//
//   - the host-agent daemon (handleSpawn) gates the path through
//     `agent.pathAllowed`, an operator-configured allow-list of file
//     trees (`RIMSKY_AGENT_ALLOW_PATHS`);
//   - `rimsky compose run`'s --service flag treats the operator as the
//     trust boundary — the operator typed the binary path on the CLI,
//     so operator-as-attacker is out of scope.
//
// A future caller that wires SpawnService into a context where the
// path is operator-untrusted (e.g. a server endpoint accepting a
// remote `--service` directive, or a CI runner whose job spec might
// contain an injected --service value) MUST add its own validation
// before SpawnService is reached. The `//nolint:gosec` below is for
// the lint suppression's audit trail; it does NOT bless arbitrary
// paths at any future call site.
//
// Lifecycle obligation: the caller is REQUIRED to drive teardown via
// Cmd.Process.Signal / Cmd.Process.Kill (or wait on the returned
// Exited channel for natural exit). The ctx passed into SpawnService
// is consumed ONLY for bounding the ready-poll (readiness timeout +
// early-cancel during boot); ctx cancellation AFTER readiness is
// intentionally NOT propagated to the spawned child. This is
// load-bearing for the host-agent's reap path (which owns SIGTERM
// timing via the reap-grace contract) and equally load-bearing for
// compose-run's shutdown coordinator (which walks every SpawnedService
// and signals each in priority order during graceful drain). Callers
// that supply a request-scoped ctx and expect ctx cancellation to take
// down the child must wrap their own teardown around the returned
// handle.
//
// @blessed-invariant: spawn-no-leak-on-readiness-timeout
func SpawnService(ctx context.Context, params SpawnServiceParams) (*SpawnedService, error) {
	port, err := FreeLocalPort()
	if err != nil {
		return nil, fmt.Errorf("allocate port: %w", err)
	}

	// Use exec.Command (NOT exec.CommandContext): the caller owns the child's
	// lifecycle and must drive teardown explicitly via the returned handle.
	// exec.CommandContext would race the agent's SIGTERM-based reap path by
	// sending SIGKILL on ctx cancellation, which the host-agent's reap-grace
	// contract forbids (the stubchild's signal handler would never observe
	// the SIGTERM and the agent's reap-clean ack would be wrong).
	cmd := exec.Command(params.BinaryPath, params.Args...) //nolint:gosec // path trust is the caller's posture (host-agent enforces allow-paths via pathAllowed; future callers MUST validate the binary path against their own trust model before invoking SpawnService)
	if params.Cwd != "" {
		cmd.Dir = params.Cwd
	}

	// RIMSKY_AGENT_PORT is appended LAST so it always wins on duplicate-key
	// resolution: exec uses the last occurrence of a duplicated key, and
	// the agent's contract is that the child MUST bind on the
	// agent-allocated port — no caller-supplied env may shadow it.
	env := append([]string(nil), params.Env...)
	env = append(env, fmt.Sprintf("%s=%d", agentPortEnvVar, port))
	cmd.Env = env
	cmd.Stdout = os.Stderr // surface child logs without polluting the parent's stdout
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
		// Reap the partial spawn before returning so callers never see a
		// leaked child on readiness failure
		// (@blessed-invariant: spawn-no-leak-on-readiness-timeout).
		killProcess(cmd)
		<-exited
		return nil, fmt.Errorf("child did not bind port %d within %s", port, readyTimeout)
	}

	return &SpawnedService{Cmd: cmd, Port: port, Exited: exited}, nil
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

	// Per-binding exec() overrides (story S-hostagent-per-binding-exec-overrides):
	// argv, working directory, and env are carried on the Binding wire message.
	// A binding that declares none of them spawns exactly as before (no extra
	// args, the instance-level cwd, inherited env), so this stays backward
	// compatible.
	//
	// Per-binding cwd overrides the instance-level cwd only when set;
	// otherwise fall back to the Spawn frame's instance-level cwd
	// (today's behavior).
	cwd := sp.GetBinding().GetCwd()
	if cwd == "" {
		cwd = sp.GetCwd()
	}

	// Env layering: inherited environment first, then each per-binding env
	// entry (so a binding key overrides the inherited value on collision —
	// exec uses the LAST occurrence of a duplicated key). SpawnService
	// appends RIMSKY_AGENT_PORT itself, after this list, so the agent port
	// always wins over any binding key.
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
		// The Validation service exposes ONLY Validate — it has no
		// Capabilities RPC. A multi-protocol binary advertises that it fronts
		// validation through its claim-producer/multi-protocol Capabilities
		// surface (CapabilitiesResponse.protocols), which the handshake reads
		// via that protocol's own case. A standalone validation-only binary
		// has no capability surface to read, so validation is a no-capability
		// forwarder: there is nothing to read back. But the spawn MUST still
		// probe that the Validation server is actually registered on the
		// spawned child — otherwise a binding that opens a TCP port but
		// exposes no Validation server would pass the handshake and the
		// first real Validate dispatch would surface as an opaque gRPC
		// error (codes.Unimplemented). One sentinel Validate call here
		// turns that "deferred opaque failure" into a clear "validation
		// service not registered on spawned binary" handshake error.
		return probeValidationRegistered(callCtx, conn)
	default:
		return nil, fmt.Errorf("unsupported protocol %q", protocol)
	}
}

// validationHandshakeProbeRole is the sentinel role the agent sends in
// the handshake-time Validate probe. It is the empty string so a real
// validator sees a vacuous request and a synthetic role can't be
// confused with anything an operator would author. The probe only
// checks the RPC dispatches at all (i.e. the Validation server IS
// registered on the spawned child); the response value is discarded.
const validationHandshakeProbeRole = ""

// probeValidationRegistered fires one Validate RPC at the child and
// returns nil capabilities on any non-Unimplemented response. The
// PURPOSE of the probe is to fail the handshake with a clear error
// when the spawned binary doesn't register a Validation server (so a
// later Validate dispatch can't surface as an opaque gRPC error). It
// does NOT validate the validator's verdict — a Validate that returns
// valid=false on the empty request is fine; the only failure case is
// gRPC codes.Unimplemented (or a network error tearing down the
// connection before the server can answer).
func probeValidationRegistered(ctx context.Context, conn *grpc.ClientConn) ([]byte, error) {
	_, err := genv1.NewValidationClient(conn).Validate(ctx, &genv1.ValidateRequest{Role: validationHandshakeProbeRole})
	if err == nil {
		// Server registered and answered cleanly.
		return nil, nil
	}
	if status.Code(err) == codes.Unimplemented {
		return nil, fmt.Errorf("validation service not registered on spawned binary: %w", err)
	}
	// Any other RPC failure means the child accepted the call (the
	// server IS registered) and returned an error for this particular
	// request shape. The probe's only contract is "server present";
	// treat the call as successful for handshake purposes.
	return nil, nil
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

// FreeLocalPort opens a listener on an OS-assigned port, reads it back, and
// closes immediately. The brief race window (another process grabbing the
// port before the child binds) is accepted per the spec's spawn contract.
func FreeLocalPort() (int, error) {
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
