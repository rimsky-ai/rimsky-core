// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package hostagent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func connectAgentToFakeProxy(t *testing.T, fp *fakeProxy, cfg Config) {
	t.Helper()
	cfg.ProxyURL = fp.addr
	if cfg.APIKey == "" {
		cfg.APIKey = "k"
	}
	runAgentInBackground(t, cfg)
	fp.waitConnected(t)
}

func spawnVia(t *testing.T, fp *fakeProxy, sp *genv1.Spawn) *genv1.SpawnAck {
	t.Helper()
	fp.sendToAgent(t, &genv1.ServerFrame{Body: &genv1.ServerFrame_Spawn{Spawn: sp}})
	frame := fp.nextClientFrame(t)
	ack := frame.GetSpawnAck()
	if ack == nil {
		t.Fatalf("expected SpawnAck, got %T", frame.GetBody())
	}
	return ack
}

func reapVia(t *testing.T, fp *fakeProxy, spawnID string, graceSec int32) *genv1.Reaped {
	t.Helper()
	fp.sendToAgent(t, &genv1.ServerFrame{Body: &genv1.ServerFrame_Reap{Reap: &genv1.Reap{
		SpawnId:             spawnID,
		SigtermGraceSeconds: graceSec,
	}}})
	frame := fp.nextClientFrame(t)
	reaped := frame.GetReaped()
	if reaped == nil {
		t.Fatalf("expected Reaped, got %T", frame.GetBody())
	}
	return reaped
}

func nextDispatch(t *testing.T, fp *fakeProxy, streamID string) *genv1.DispatchFrame {
	t.Helper()
	frame := fp.nextClientFrame(t)
	df := frame.GetDispatchFrame()
	if df == nil {
		t.Fatalf("expected DispatchFrame, got %T", frame.GetBody())
	}
	if df.GetStreamId() != streamID {
		t.Fatalf("dispatch stream_id = %q, want %q", df.GetStreamId(), streamID)
	}
	return df
}

func TestSpawnReadyCapabilitiesHandshake(t *testing.T) {
	bin := buildStubChild(t)
	fp := startFakeProxy(t)
	connectAgentToFakeProxy(t, fp, Config{})

	spawnID := uuid.NewString()
	ack := spawnVia(t, fp, &genv1.Spawn{
		SpawnId:             spawnID,
		Binding:             &genv1.Binding{Path: bin},
		ExpectedProtocols:   []string{protocolExecutor, protocolClaimProducer},
		ReadyTimeoutSeconds: 15,
	})

	if ack.GetStatus() != genv1.SpawnAck_SPAWN_STATUS_READY {
		t.Fatalf("status = %v, want READY (err=%v)", ack.GetStatus(), ack.GetError())
	}
	if ack.GetSpawnId() != spawnID {
		t.Fatalf("spawn_id = %q, want %q", ack.GetSpawnId(), spawnID)
	}

	execCaps := ack.GetCapabilities()[protocolExecutor]
	if execCaps == nil {
		t.Fatal("missing executor capabilities")
	}
	var obs genv1.ObservabilityCapabilities
	if err := proto.Unmarshal(execCaps, &obs); err != nil {
		t.Fatalf("unmarshal executor caps: %v", err)
	}
	if len(obs.GetDeclaredTags()) != 1 || obs.GetDeclaredTags()[0] != "stubchild.output" {
		t.Fatalf("declared tags = %v, want [stubchild.output]", obs.GetDeclaredTags())
	}

	cpCaps := ack.GetCapabilities()[protocolClaimProducer]
	if cpCaps == nil {
		t.Fatal("missing claim_producer capabilities")
	}
	var cp genv1.CapabilitiesResponse
	if err := proto.Unmarshal(cpCaps, &cp); err != nil {
		t.Fatalf("unmarshal claim-producer caps: %v", err)
	}
	if len(cp.GetWriteSemanticsAllowed()) != 1 {
		t.Fatalf("write semantics = %v, want one entry", cp.GetWriteSemanticsAllowed())
	}

	reapVia(t, fp, spawnID, 5)
}

func TestSpawnRejectedByAllowPaths(t *testing.T) {
	bin := buildStubChild(t)
	fp := startFakeProxy(t)
	connectAgentToFakeProxy(t, fp, Config{AllowPaths: []string{"/usr/local/bin/*"}})

	ack := spawnVia(t, fp, &genv1.Spawn{
		SpawnId:             uuid.NewString(),
		Binding:             &genv1.Binding{Path: bin},
		ExpectedProtocols:   []string{protocolExecutor},
		ReadyTimeoutSeconds: 5,
	})
	if ack.GetStatus() != genv1.SpawnAck_SPAWN_STATUS_FAILED {
		t.Fatalf("status = %v, want FAILED", ack.GetStatus())
	}
	if ack.GetError().GetClass() != errClassSpawnFailed {
		t.Fatalf("error class = %q, want %q", ack.GetError().GetClass(), errClassSpawnFailed)
	}
}

func copyExecutable(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	if err := os.WriteFile(dst, data, 0o755); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
}

func TestSpawnDeniesSymlinkEscapingAllowedDir(t *testing.T) {
	realBin := buildStubChild(t)
	allowedDir := t.TempDir()
	link := filepath.Join(allowedDir, "stubchild")
	if err := os.Symlink(realBin, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	fp := startFakeProxy(t)
	connectAgentToFakeProxy(t, fp, Config{AllowPaths: []string{filepath.Join(allowedDir, "*")}})

	ack := spawnVia(t, fp, &genv1.Spawn{
		SpawnId:             uuid.NewString(),
		Binding:             &genv1.Binding{Path: link},
		ExpectedProtocols:   []string{protocolExecutor},
		ReadyTimeoutSeconds: 5,
	})
	if ack.GetStatus() != genv1.SpawnAck_SPAWN_STATUS_FAILED {
		t.Fatalf("status = %v, want FAILED (a symlink under the allowed dir pointing outside it must resolve to its real target and be denied)", ack.GetStatus())
	}
}

func TestSpawnAllowsRealBinaryInsideAllowedDir(t *testing.T) {
	realBin := buildStubChild(t)
	allowedDir := t.TempDir()
	inside := filepath.Join(allowedDir, "stubchild")
	copyExecutable(t, realBin, inside)

	fp := startFakeProxy(t)
	connectAgentToFakeProxy(t, fp, Config{AllowPaths: []string{filepath.Join(allowedDir, "*")}})

	spawnID := uuid.NewString()
	ack := spawnVia(t, fp, &genv1.Spawn{
		SpawnId:             spawnID,
		Binding:             &genv1.Binding{Path: inside},
		ExpectedProtocols:   []string{protocolExecutor},
		ReadyTimeoutSeconds: 15,
	})
	if ack.GetStatus() != genv1.SpawnAck_SPAWN_STATUS_READY {
		t.Fatalf("status = %v, want READY (a real binary inside the allowed dir must be permitted) err=%v", ack.GetStatus(), ack.GetError())
	}
	reapVia(t, fp, spawnID, 5)
}

func TestSpawnDeniesRelativeAllowPatternAgainstForeignBinary(t *testing.T) {
	foreignBin := buildStubChild(t)
	foreignDir := filepath.Dir(foreignBin)

	fp := startFakeProxy(t)
	connectAgentToFakeProxy(t, fp, Config{AllowPaths: []string{"stubchild"}})

	ack := spawnVia(t, fp, &genv1.Spawn{
		SpawnId:             uuid.NewString(),
		Binding:             &genv1.Binding{Path: "stubchild", Cwd: foreignDir},
		ExpectedProtocols:   []string{protocolExecutor},
		ReadyTimeoutSeconds: 5,
	})
	if ack.GetStatus() != genv1.SpawnAck_SPAWN_STATUS_FAILED {
		t.Fatalf("status = %v, want FAILED (a relative allow pattern must anchor to the agent cwd, not a remote-supplied binding cwd, so it cannot match a foreign same-named binary)", ack.GetStatus())
	}
}

func occupyPortWithoutListening(t *testing.T) (int, func()) {
	t.Helper()
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socket: %v", err)
	}
	if err := syscall.Bind(fd, &syscall.SockaddrInet4{Addr: [4]byte{127, 0, 0, 1}}); err != nil {
		_ = syscall.Close(fd)
		t.Fatalf("bind: %v", err)
	}
	sa, err := syscall.Getsockname(fd)
	if err != nil {
		_ = syscall.Close(fd)
		t.Fatalf("getsockname: %v", err)
	}
	return sa.(*syscall.SockaddrInet4).Port, func() { _ = syscall.Close(fd) }
}

func TestSpawnService_TrustsCallerSuppliedPathWithNoInternalAllowlistCheck(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	untrustedPath := filepath.Join(t.TempDir(), "does-not-exist-and-is-not-allowlisted")
	_, err := SpawnService(ctx, SpawnServiceParams{
		BinaryPath:   untrustedPath,
		ReadyTimeout: time.Second,
	})
	if err == nil {
		t.Fatal("expected an error for a nonexistent binary path")
	}
	if strings.Contains(err.Error(), "not permitted") || strings.Contains(err.Error(), "allow-paths") {
		t.Fatalf("SpawnService must not itself apply an allow-paths policy (that is resolveBindingPath's job, called before SpawnService); "+
			"got a policy-shaped rejection instead of a plain exec error: %v", err)
	}
	if !errors.Is(err, os.ErrNotExist) && !strings.Contains(err.Error(), "exec start") {
		t.Fatalf("expected a plain OS/exec error for the unresolvable path, got: %v", err)
	}
}

func TestSpawnServiceRetriesPastStolenPort(t *testing.T) {
	bin := buildFixture(t, "stub-service")
	stolenPort, release := occupyPortWithoutListening(t)
	defer release()

	calls := 0
	portSource := func() (int, error) {
		calls++
		if calls == 1 {
			return stolenPort, nil
		}
		return FreeLocalPort()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	spawned, err := SpawnService(ctx, SpawnServiceParams{
		BinaryPath:   bin,
		Env:          os.Environ(),
		ReadyTimeout: 2 * time.Second,
		portSource:   portSource,
	})
	if err != nil {
		t.Fatalf("SpawnService: expected a bounded retry to re-pick a fresh port and succeed past the occupied port, got %v", err)
	}
	if spawned.Port == stolenPort {
		t.Fatalf("spawned on the occupied port %d, expected a re-picked free port", stolenPort)
	}
	if calls < 2 {
		t.Fatalf("portSource called %d time(s), want >= 2 (a collision on the first port must trigger a re-pick)", calls)
	}

	if err := spawned.Cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal child: %v", err)
	}
	<-spawned.Exited
}

func TestSpawnBinaryMissing(t *testing.T) {
	fp := startFakeProxy(t)
	connectAgentToFakeProxy(t, fp, Config{})

	missing := filepath.Join(t.TempDir(), "does-not-exist")
	ack := spawnVia(t, fp, &genv1.Spawn{
		SpawnId:             uuid.NewString(),
		Binding:             &genv1.Binding{Path: missing},
		ExpectedProtocols:   []string{protocolExecutor},
		ReadyTimeoutSeconds: 2,
	})
	if ack.GetStatus() != genv1.SpawnAck_SPAWN_STATUS_FAILED {
		t.Fatalf("status = %v, want FAILED", ack.GetStatus())
	}
	if ack.GetError().GetClass() != errClassSpawnFailed {
		t.Fatalf("error class = %q, want %q", ack.GetError().GetClass(), errClassSpawnFailed)
	}
}

func TestSpawnReadyTimeout(t *testing.T) {
	bin := buildStubChild(t)
	t.Setenv("STUBCHILD_NO_BIND", "1")
	fp := startFakeProxy(t)
	connectAgentToFakeProxy(t, fp, Config{})

	ack := spawnVia(t, fp, &genv1.Spawn{
		SpawnId:             uuid.NewString(),
		Binding:             &genv1.Binding{Path: bin},
		ExpectedProtocols:   []string{protocolExecutor},
		ReadyTimeoutSeconds: 1,
	})
	if ack.GetStatus() != genv1.SpawnAck_SPAWN_STATUS_FAILED {
		t.Fatalf("status = %v, want FAILED", ack.GetStatus())
	}
	if msg := ack.GetError().GetMessage(); !strings.Contains(msg, "did not bind port") || !strings.Contains(msg, "within") {
		t.Fatalf("error message = %q, want a genuine-timeout message mentioning it did not bind within the deadline", msg)
	}
}

func TestSpawnServiceChildExitedBeforeBindingIsDistinguishedFromTimeout(t *testing.T) {
	bin := buildStubChild(t)
	t.Setenv("STUBCHILD_EXIT_IMMEDIATELY", "1")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_, err := SpawnService(ctx, SpawnServiceParams{
		BinaryPath:   bin,
		Env:          os.Environ(),
		ReadyTimeout: 10 * time.Second,
	})
	if err == nil {
		t.Fatal("SpawnService: expected an error, got nil")
	}
	if !errors.Is(err, errChildDidNotBind) {
		t.Fatalf("SpawnService error = %v, want it to wrap errChildDidNotBind", err)
	}
	if !strings.Contains(err.Error(), "exited before binding port") {
		t.Fatalf("SpawnService error = %q, want a message distinguishing an early exit from a readiness timeout", err.Error())
	}
	if strings.Contains(err.Error(), "did not bind port") && strings.Contains(err.Error(), "within") {
		t.Fatalf("SpawnService error = %q, must not reuse the timeout wording for a child that exited immediately", err.Error())
	}
}

func TestSpawnServiceCtxCancelDuringPortWaitIsNotClassifiedAsBindFailure(t *testing.T) {
	bin := buildStubChild(t)
	t.Setenv("STUBCHILD_NO_BIND", "1")

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer waitCancel()

	_, err := SpawnService(waitCtx, SpawnServiceParams{
		BinaryPath:   bin,
		Env:          os.Environ(),
		ReadyTimeout: 10 * time.Second,
	})
	if err == nil {
		t.Fatal("SpawnService: expected an error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("SpawnService error = %v, want context.DeadlineExceeded (the caller context, not a readiness timeout)", err)
	}
	if errors.Is(err, errChildDidNotBind) {
		t.Fatalf("SpawnService error = %v, must not be classified as errChildDidNotBind (would trigger a pointless retry after the caller already gave up)", err)
	}
}

func TestDispatchExecutorReturnsUnaryOutcome(t *testing.T) {
	bin := buildStubChild(t)
	t.Setenv("STUBCHILD_EXECUTE_ECHO", "1")
	fp := startFakeProxy(t)
	connectAgentToFakeProxy(t, fp, Config{})

	spawnID := uuid.NewString()
	ack := spawnVia(t, fp, &genv1.Spawn{
		SpawnId:             spawnID,
		Binding:             &genv1.Binding{Path: bin},
		ExpectedProtocols:   []string{protocolExecutor},
		ReadyTimeoutSeconds: 15,
	})
	if ack.GetStatus() != genv1.SpawnAck_SPAWN_STATUS_READY {
		t.Fatalf("spawn failed: %v", ack.GetError())
	}

	reqBytes, _ := proto.Marshal(&genv1.ExecuteRequest{NodeId: "node-7", InstanceId: "inst-1"})
	streamID := uuid.NewString()
	fp.sendToAgent(t, &genv1.ServerFrame{Body: &genv1.ServerFrame_DispatchFrame{DispatchFrame: &genv1.DispatchFrame{
		SpawnId:  spawnID,
		Protocol: protocolExecutor,
		Payload:  reqBytes,
		StreamId: streamID,
		Kind:     genv1.DispatchFrame_DISPATCH_FRAME_KIND_DATA,
	}}})

	resp := nextDispatch(t, fp, streamID)
	if resp.GetKind() != genv1.DispatchFrame_DISPATCH_FRAME_KIND_DATA {
		t.Fatalf("kind = %v, want DATA (Outcome should not be CANCEL)", resp.GetKind())
	}
	var outcome genv1.Outcome
	if err := proto.Unmarshal(resp.GetPayload(), &outcome); err != nil {
		t.Fatalf("unmarshal outcome: %v", err)
	}
	success := outcome.GetSuccess()
	if success == nil {
		t.Fatalf("expected Outcome{Success}, got %T", outcome.GetOutcome())
	}
	if len(success.GetTags()) != 1 || success.GetTags()[0] != "stubchild.output" {
		t.Fatalf("tags = %v, want [stubchild.output]", success.GetTags())
	}
	delta := success.GetAttributesDelta().AsMap()
	if delta["echoed_node_id"] != "node-7" {
		t.Fatalf("attributes_delta[echoed_node_id] = %v, want node-7", delta["echoed_node_id"])
	}

	reapVia(t, fp, spawnID, 5)
}

func TestDispatchClaimProducerUnary(t *testing.T) {
	bin := buildStubChild(t)
	fp := startFakeProxy(t)
	connectAgentToFakeProxy(t, fp, Config{})

	spawnID := uuid.NewString()
	ack := spawnVia(t, fp, &genv1.Spawn{
		SpawnId:             spawnID,
		Binding:             &genv1.Binding{Path: bin},
		ExpectedProtocols:   []string{protocolClaimProducer},
		ReadyTimeoutSeconds: 15,
	})
	if ack.GetStatus() != genv1.SpawnAck_SPAWN_STATUS_READY {
		t.Fatalf("spawn failed: %v", ack.GetError())
	}

	openBytes, _ := proto.Marshal(&genv1.OpenRequest{ClaimId: "claim-1", ProducerName: "fs", InstanceId: "inst-1"})
	streamID := uuid.NewString()
	fp.sendToAgent(t, &genv1.ServerFrame{Body: &genv1.ServerFrame_DispatchFrame{DispatchFrame: &genv1.DispatchFrame{
		SpawnId:           spawnID,
		Protocol:          protocolClaimProducer,
		Payload:           openBytes,
		StreamId:          streamID,
		Kind:              genv1.DispatchFrame_DISPATCH_FRAME_KIND_DATA,
		ClaimProducerVerb: genv1.DispatchFrame_CLAIM_PRODUCER_VERB_OPEN,
	}}})

	resp := nextDispatch(t, fp, streamID)
	var open genv1.OpenResponse
	if err := proto.Unmarshal(resp.GetPayload(), &open); err != nil {
		t.Fatalf("unmarshal open response: %v", err)
	}
	if open.GetAcquired() == nil {
		t.Fatalf("expected Acquired result, got %v", open.GetResult())
	}

	reapVia(t, fp, spawnID, 5)
}

func TestDispatchClaimProducerVerbFidelity(t *testing.T) {
	verbLog := filepath.Join(t.TempDir(), "verbs.log")
	t.Setenv("STUBCHILD_VERB_LOG", verbLog)

	bin := buildStubChild(t)
	fp := startFakeProxy(t)
	connectAgentToFakeProxy(t, fp, Config{})

	spawnID := uuid.NewString()
	ack := spawnVia(t, fp, &genv1.Spawn{
		SpawnId:             spawnID,
		Binding:             &genv1.Binding{Path: bin},
		ExpectedProtocols:   []string{protocolClaimProducer},
		ReadyTimeoutSeconds: 15,
	})
	if ack.GetStatus() != genv1.SpawnAck_SPAWN_STATUS_READY {
		t.Fatalf("spawn failed: %v", ack.GetError())
	}

	abandonBytes, _ := proto.Marshal(&genv1.AbandonRequest{ClaimId: "claim-1"})
	abandonStream := uuid.NewString()
	fp.sendToAgent(t, &genv1.ServerFrame{Body: &genv1.ServerFrame_DispatchFrame{DispatchFrame: &genv1.DispatchFrame{
		SpawnId:           spawnID,
		Protocol:          protocolClaimProducer,
		Payload:           abandonBytes,
		StreamId:          abandonStream,
		Kind:              genv1.DispatchFrame_DISPATCH_FRAME_KIND_DATA,
		ClaimProducerVerb: genv1.DispatchFrame_CLAIM_PRODUCER_VERB_ABANDON,
	}}})
	abandonResp := nextDispatch(t, fp, abandonStream)
	if abandonResp.GetKind() == genv1.DispatchFrame_DISPATCH_FRAME_KIND_CANCEL {
		t.Fatalf("abandon dispatch was cancelled (agent rejected the verb)")
	}

	releaseBytes, _ := proto.Marshal(&genv1.ReleaseRequest{ClaimId: "claim-1"})
	releaseStream := uuid.NewString()
	fp.sendToAgent(t, &genv1.ServerFrame{Body: &genv1.ServerFrame_DispatchFrame{DispatchFrame: &genv1.DispatchFrame{
		SpawnId:           spawnID,
		Protocol:          protocolClaimProducer,
		Payload:           releaseBytes,
		StreamId:          releaseStream,
		Kind:              genv1.DispatchFrame_DISPATCH_FRAME_KIND_DATA,
		ClaimProducerVerb: genv1.DispatchFrame_CLAIM_PRODUCER_VERB_RELEASE,
	}}})
	releaseResp := nextDispatch(t, fp, releaseStream)
	if releaseResp.GetKind() == genv1.DispatchFrame_DISPATCH_FRAME_KIND_CANCEL {
		t.Fatalf("release dispatch was cancelled (agent rejected the verb)")
	}

	reapVia(t, fp, spawnID, 5)

	logged := readVerbLog(t, verbLog)
	if got := strings.Join(logged, ","); got != "abandon,release" {
		t.Fatalf("child saw verbs %q, want %q (a Commit here is the state-integrity bug)", got, "abandon,release")
	}
}

func readVerbLog(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read verb log: %v", err)
	}
	var out []string
	for _, line := range strings.Split(string(raw), "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func TestReapTerminatesChild(t *testing.T) {
	bin := buildStubChild(t)
	fp := startFakeProxy(t)
	connectAgentToFakeProxy(t, fp, Config{})

	spawnID := uuid.NewString()
	ack := spawnVia(t, fp, &genv1.Spawn{
		SpawnId:             spawnID,
		Binding:             &genv1.Binding{Path: bin},
		ExpectedProtocols:   []string{protocolExecutor},
		ReadyTimeoutSeconds: 15,
	})
	if ack.GetStatus() != genv1.SpawnAck_SPAWN_STATUS_READY {
		t.Fatalf("spawn failed: %v", ack.GetError())
	}

	reaped := reapVia(t, fp, spawnID, 5)
	if reaped.GetSpawnId() != spawnID {
		t.Fatalf("reaped spawn_id = %q, want %q", reaped.GetSpawnId(), spawnID)
	}
	if !reaped.GetClean() {
		t.Fatal("expected clean reap (stubchild exits 0 on SIGTERM)")
	}
}

func TestConfigDefaults(t *testing.T) {
	c := Config{}.withDefaults()
	if c.HeartbeatInterval != 10*time.Second {
		t.Fatalf("heartbeat = %v, want 10s", c.HeartbeatInterval)
	}
	if c.ReapGracePeriod != 30*time.Second {
		t.Fatalf("reap grace = %v, want 30s", c.ReapGracePeriod)
	}
	if c.AgentLabel == "" {
		t.Fatal("agent label should default to hostname-pid")
	}
	idx := strings.LastIndex(c.AgentLabel, "-")
	if idx < 0 {
		t.Fatalf("agent label %q does not have a hostname-pid shape (no trailing dash)", c.AgentLabel)
	}
	pidPart := c.AgentLabel[idx+1:]
	pid, err := strconv.Atoi(pidPart)
	if err != nil {
		t.Fatalf("agent label %q pid suffix %q is not numeric: %v", c.AgentLabel, pidPart, err)
	}
	if pid != os.Getpid() {
		t.Fatalf("agent label %q pid suffix = %d, want current pid %d", c.AgentLabel, pid, os.Getpid())
	}
}
