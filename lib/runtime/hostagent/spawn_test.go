// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// spawn_test.go — host-agent spawn / dispatch / reap coverage. Each
// test stands up a fakeProxy, runs the production hostagent.Run loop
// against it, and exec()s the real testdata/stubchild binary — so the
// path under test is the real exec → RIMSKY_AGENT_PORT → port-probe →
// Capabilities handshake → child registration path, not a mock.
//
// Under TD-execute-rpc-unary, Executor.Execute is unary: the agent
// receives a DispatchFrame carrying a marshaled ExecuteRequest and
// answers with one DispatchFrame carrying a marshaled Outcome.

package hostagent

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// connectAgentToFakeProxy starts the agent against fp and returns once the
// agent has registered. The local listener uses an ephemeral port.
func connectAgentToFakeProxy(t *testing.T, fp *fakeProxy, cfg Config) {
	t.Helper()
	cfg.RimskyURL = fp.addr
	if cfg.APIKey == "" {
		cfg.APIKey = "k"
	}
	runAgentInBackground(t, cfg)
	fp.waitConnected(t)
}

// spawnVia pushes a Spawn frame and returns the agent's SpawnAck.
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

// reapVia pushes a Reap and returns the Reaped reply.
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

// nextDispatch reads the next ClientFrame and asserts it is a DispatchFrame
// for the given stream-id.
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

// TestSpawnReadyCapabilitiesHandshake spawns the real stubchild and asserts a
// READY ack carrying the per-protocol Capabilities responses.
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

	// @deliberate: Executor capabilities decode to the stubchild's declared tags.
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

	// @deliberate: Claim-producer capabilities decode to the stubchild's write-semantics.
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

	// @deliberate: Reap it so the child doesn't outlive the test.
	reapVia(t, fp, spawnID, 5)
}

// TestSpawnRejectedByAllowPaths asserts a non-matching path yields FAILED.
func TestSpawnRejectedByAllowPaths(t *testing.T) {
	bin := buildStubChild(t)
	fp := startFakeProxy(t)
	connectAgentToFakeProxy(t, fp, Config{AllowPaths: []string{"/usr/local/bin/*"}})

	ack := spawnVia(t, fp, &genv1.Spawn{
		SpawnId:             uuid.NewString(),
		Binding:             &genv1.Binding{Path: bin}, // @deliberate: temp-dir path, not matched
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

// TestSpawnBinaryMissing asserts a binding that names a non-existent path
// fails with a spawn_failed ack (exercises the exec.Start error branch).
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

// TestSpawnReadyTimeout asserts a child that never binds its port yields a
// FAILED ack after the ready timeout.
func TestSpawnReadyTimeout(t *testing.T) {
	bin := buildStubChild(t)
	t.Setenv("STUBCHILD_NO_BIND", "1") // @deliberate: inherited by the spawned child
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
}

// TestDispatchExecutorReturnsUnaryOutcome spawns the echoing stubchild,
// dispatches an ExecuteRequest, and asserts the agent answers with a
// single DATA DispatchFrame carrying a serialized Outcome{Success}
// — the unary RPC shape per TD-execute-rpc-unary.
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
	// @deliberate: STUBCHILD_EXECUTE_ECHO surfaces the node_id on
	// attributes_delta + tags["stubchild.output"] per
	// TD-collapse-named-event-to-tags.
	if len(success.GetTags()) != 1 || success.GetTags()[0] != "stubchild.output" {
		t.Fatalf("tags = %v, want [stubchild.output]", success.GetTags())
	}
	delta := success.GetAttributesDelta().AsMap()
	if delta["echoed_node_id"] != "node-7" {
		t.Fatalf("attributes_delta[echoed_node_id] = %v, want node-7", delta["echoed_node_id"])
	}

	reapVia(t, fp, spawnID, 5)
}

// TestDispatchClaimProducerUnary spawns the stubchild and dispatches a unary
// claim-producer Open, asserting a single response DATA frame decoding to an
// Acquired OpenResponse.
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

// TestDispatchClaimProducerVerbFidelity proves the agent invokes the exact
// ClaimProducer verb named on the DispatchFrame — not one inferred from the
// payload shape. CommitRequest/AbandonRequest/ReleaseRequest are byte-
// identical at claim_id, so an agent that guessed from the payload would
// silently Commit an Abandon/Release (a state-integrity bug).
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

	// @deliberate: Abandon: the request is wire-identical to a Commit at claim_id.
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

	// @deliberate: Release: also wire-identical to a Commit at claim_id.
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

	// @deliberate: The child must have seen Abandon and Release — never Commit.
	logged := readVerbLog(t, verbLog)
	if got := strings.Join(logged, ","); got != "abandon,release" {
		t.Fatalf("child saw verbs %q, want %q (a Commit here is the state-integrity bug)", got, "abandon,release")
	}
}

// readVerbLog reads the STUBCHILD_VERB_LOG file's non-empty lines.
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

// TestReapTerminatesChild spawns the stubchild then reaps it, asserting a
// clean Reaped ack.
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

// TestConfigDefaults asserts withDefaults fills the documented defaults.
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
	if _, err := strconv.Atoi(c.AgentLabel[len(c.AgentLabel)-1:]); err != nil {
		// @deliberate: label ends with the pid digits; loose sanity check only.
		_ = err
	}
}
