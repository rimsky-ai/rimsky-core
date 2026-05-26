// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

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

	genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
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

	// Executor capabilities decode to the stubchild's declared events.
	execCaps := ack.GetCapabilities()[protocolExecutor]
	if execCaps == nil {
		t.Fatal("missing executor capabilities")
	}
	var obs genv1.ObservabilityCapabilities
	if err := proto.Unmarshal(execCaps, &obs); err != nil {
		t.Fatalf("unmarshal executor caps: %v", err)
	}
	if len(obs.GetDeclaredEvents()) != 1 || obs.GetDeclaredEvents()[0] != "stubchild.output" {
		t.Fatalf("declared events = %v, want [stubchild.output]", obs.GetDeclaredEvents())
	}

	// Claim-producer capabilities decode to the stubchild's write-semantics.
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

	// Reap it so the child doesn't outlive the test.
	reapVia(t, fp, spawnID, 5)
}

// TestSpawnRejectedByAllowPaths asserts a non-matching path yields FAILED.
func TestSpawnRejectedByAllowPaths(t *testing.T) {
	bin := buildStubChild(t)
	fp := startFakeProxy(t)
	connectAgentToFakeProxy(t, fp, Config{AllowPaths: []string{"/usr/local/bin/*"}})

	ack := spawnVia(t, fp, &genv1.Spawn{
		SpawnId:             uuid.NewString(),
		Binding:             &genv1.Binding{Path: bin}, // temp-dir path, not matched
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

// TestSpawnReadyTimeout asserts a child that never binds its port yields a
// FAILED ack after the ready timeout (the stubchild honors STUBCHILD_NO_BIND
// via env — but env is inherited; we instead point at a bare path that exists
// but never serves by using a short timeout and the no-bind knob through the
// agent's inherited env).
func TestSpawnReadyTimeout(t *testing.T) {
	bin := buildStubChild(t)
	t.Setenv("STUBCHILD_NO_BIND", "1") // inherited by the spawned child
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

// TestDispatchExecutorStreaming spawns the echoing stubchild, dispatches an
// ExecuteRequest, and asserts the agent streams a NamedEvent then a terminal
// StreamClose back as DATA frames.
func TestDispatchExecutorStreaming(t *testing.T) {
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

	// First DATA frame: the echoed NamedEvent.
	first := nextDispatch(t, fp, streamID)
	var ev1 genv1.ExecuteEvent
	if err := proto.Unmarshal(first.GetPayload(), &ev1); err != nil {
		t.Fatalf("unmarshal event 1: %v", err)
	}
	named, ok := ev1.GetEvent().(*genv1.ExecuteEvent_NamedEvent)
	if !ok {
		t.Fatalf("event 1 = %T, want NamedEvent", ev1.GetEvent())
	}
	if string(named.NamedEvent.GetPayload()) != "node-7" {
		t.Fatalf("echoed payload = %q, want node-7", named.NamedEvent.GetPayload())
	}

	// Second DATA frame: the terminal StreamClose.
	second := nextDispatch(t, fp, streamID)
	var ev2 genv1.ExecuteEvent
	if err := proto.Unmarshal(second.GetPayload(), &ev2); err != nil {
		t.Fatalf("unmarshal event 2: %v", err)
	}
	if _, ok := ev2.GetEvent().(*genv1.ExecuteEvent_StreamClose); !ok {
		t.Fatalf("event 2 = %T, want StreamClose", ev2.GetEvent())
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
// silently Commit an Abandon/Release (a state-integrity bug). The stubchild
// records each invoked verb to STUBCHILD_VERB_LOG; we dispatch Abandon then
// Release and assert the child saw exactly those verbs.
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

	// Abandon: the request is wire-identical to a Commit at claim_id.
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

	// Release: also wire-identical to a Commit at claim_id.
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

	// The child must have seen Abandon and Release — never Commit.
	logged := readVerbLog(t, verbLog)
	if got := joinVerbs(logged); got != "abandon,release" {
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

// joinVerbs joins verbs with commas for a stable assertion string.
func joinVerbs(verbs []string) string { return strings.Join(verbs, ",") }

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
		// label ends with the pid digits; loose sanity check only.
		_ = err
	}
}
