// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// dispatch_unary_test.go — host-agent-side unit coverage for
// `dispatchUnaryByMethod` and the three per-protocol forwarders
// (`forwardPublisherUnary`, `forwardValidationUnary`,
// `forwardDataProcessingUnary`). The scenario test under
// test/scenarios/host_agent_latebind_all_protocols_test.go drives the
// same wire path end-to-end through the supervisor-facing proxy; this
// file pins the routing-table contract closer to the unit it tests,
// so a regression in `dispatchUnaryByMethod` (e.g. a swap of
// Subscribe → Unsubscribe; a typo in the rpc_method switch; an
// rpcMethod not propagating to the per-protocol forwarder) reddens a
// fast in-package unit test rather than the slower scenario harness.
//
// Each sub-test exec()s the testdata/stubchild, dispatches a real
// gRPC call into the spawned child via the agent's
// handleDispatchFrame → dispatchUnaryByMethod path, and asserts both
// the response shape AND a side-effect recorded by the stub (a log
// line / sentinel-driven return value) — so a routing slip cannot
// pass with a plausibly-shaped but wrong-RPC response.

package hostagent

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// dispatchUnaryVia pushes a unary-protocol DispatchFrame and returns the
// agent's response DATA frame. The caller supplies the protocol name,
// rpc_method, and marshaled request payload; the harness handles the
// stream-id plumbing.
func dispatchUnaryVia(t *testing.T, fp *fakeProxy, spawnID, protocol, rpcMethod string, payload []byte) *genv1.DispatchFrame {
	t.Helper()
	streamID := uuid.NewString()
	fp.sendToAgent(t, &genv1.ServerFrame{Body: &genv1.ServerFrame_DispatchFrame{DispatchFrame: &genv1.DispatchFrame{
		SpawnId:   spawnID,
		Protocol:  protocol,
		RpcMethod: rpcMethod,
		Payload:   payload,
		StreamId:  streamID,
		Kind:      genv1.DispatchFrame_DISPATCH_FRAME_KIND_DATA,
	}}})
	return nextDispatch(t, fp, streamID)
}

// spawnStubForUnaryProtocols spawns the stubchild advertising the three
// unary protocols and returns the spawn-id. Centralized so the per-RPC
// sub-tests don't re-duplicate the boilerplate.
func spawnStubForUnaryProtocols(t *testing.T, fp *fakeProxy) string {
	t.Helper()
	bin := buildStubChild(t)
	spawnID := uuid.NewString()
	ack := spawnVia(t, fp, &genv1.Spawn{
		SpawnId: spawnID,
		Binding: &genv1.Binding{Path: bin},
		ExpectedProtocols: []string{
			protocolPublisher,
			protocolValidation,
			protocolDataProcessing,
		},
		ReadyTimeoutSeconds: 15,
	})
	if ack.GetStatus() != genv1.SpawnAck_SPAWN_STATUS_READY {
		t.Fatalf("spawn failed: %v", ack.GetError())
	}
	return spawnID
}

// TestDispatchUnary_Publisher_SubscribeReachesSpawnedChild routes a real
// Publisher.Subscribe through dispatchUnaryByMethod into a live spawned
// stub binary, asserting BOTH a clean response AND the stub's
// publish-log records the dispatch. The publish-log line is the
// load-bearing observation: a routing slip that "succeeded" via the
// wrong RPC would still parse a SubscribeResponse but the stub would
// not record the line.
func TestDispatchUnary_Publisher_SubscribeReachesSpawnedChild(t *testing.T) {
	publishLog := t.TempDir() + "/publish.log"
	t.Setenv("STUBCHILD_PUBLISH_LOG", publishLog)

	fp := startFakeProxy(t)
	connectAgentToFakeProxy(t, fp, Config{})
	spawnID := spawnStubForUnaryProtocols(t, fp)
	t.Cleanup(func() { reapVia(t, fp, spawnID, 5) })

	const (
		subID      = "pub-sub-unit-1"
		instanceID = "inst-unit-1"
		targetNode = "receiver"
	)
	reqBytes, err := proto.Marshal(&genv1.SubscribeRequest{
		PublisherSubscriptionId: subID,
		InstanceId:              instanceID,
		Kind:                    "cron",
		TargetNode:              targetNode,
	})
	require.NoError(t, err)

	resp := dispatchUnaryVia(t, fp, spawnID, protocolPublisher, "Subscribe", reqBytes)
	require.Equal(t, genv1.DispatchFrame_DISPATCH_FRAME_KIND_DATA, resp.GetKind(),
		"dispatch must succeed (no CANCEL fall-through from a misrouted RPC)")

	var sub genv1.SubscribeResponse
	require.NoError(t, proto.Unmarshal(resp.GetPayload(), &sub),
		"the response payload must decode as a SubscribeResponse (not e.g. an UnsubscribeResponse from a routing slip)")

	want := strings.Join([]string{subID, instanceID, targetNode}, " ")
	logBytes, err := os.ReadFile(publishLog)
	require.NoError(t, err, "stub must have written the publish log")
	require.Contains(t, string(logBytes), want,
		"the stub publisher recorded no Subscribe — dispatchUnaryByMethod did not route Publisher.Subscribe to the spawned child")
}

// TestDispatchUnary_Publisher_UnsubscribeUsesDistinctRPC pins the
// rpc_method authoritativeness: SubscribeRequest and UnsubscribeRequest
// are distinct types, so a routing slip that swallowed rpc_method and
// fell through to e.g. Subscribe would either fail to decode the
// payload (the encoded request is an UnsubscribeRequest), or — worse —
// silently fire a Subscribe with an empty body. The stub records each
// Subscribe; an Unsubscribe leaves no publish-log line.
func TestDispatchUnary_Publisher_UnsubscribeUsesDistinctRPC(t *testing.T) {
	publishLog := t.TempDir() + "/publish.log"
	t.Setenv("STUBCHILD_PUBLISH_LOG", publishLog)

	fp := startFakeProxy(t)
	connectAgentToFakeProxy(t, fp, Config{})
	spawnID := spawnStubForUnaryProtocols(t, fp)
	t.Cleanup(func() { reapVia(t, fp, spawnID, 5) })

	reqBytes, err := proto.Marshal(&genv1.UnsubscribeRequest{
		PublisherSubscriptionId: "pub-sub-unit-unsub",
	})
	require.NoError(t, err)

	resp := dispatchUnaryVia(t, fp, spawnID, protocolPublisher, "Unsubscribe", reqBytes)
	require.Equal(t, genv1.DispatchFrame_DISPATCH_FRAME_KIND_DATA, resp.GetKind())

	var unsub genv1.UnsubscribeResponse
	require.NoError(t, proto.Unmarshal(resp.GetPayload(), &unsub),
		"the response payload must decode as an UnsubscribeResponse")

	logBytes, _ := os.ReadFile(publishLog)
	require.NotContains(t, string(logBytes), "pub-sub-unit-unsub",
		"the stub publisher recorded a Subscribe for an UnsubscribeRequest — dispatchUnaryByMethod misrouted the rpc_method")
}

// TestDispatchUnary_Validation_RejectsSentinelRole drives a real
// Validation.Validate dispatch through dispatchUnaryByMethod into the
// spawned stub. The sentinel role makes the stub REJECT (valid=false
// with a sentinel-class ValidationFinding); a routing slip that
// silently swallowed the request and returned a default-constructed
// ValidateResponse would fail this assertion because valid would be
// the proto default (false → matches; but Errors would be empty).
// Asserting on the sentinel finding's class binds the response to the
// real spawned-stub code path.
func TestDispatchUnary_Validation_RejectsSentinelRole(t *testing.T) {
	fp := startFakeProxy(t)
	connectAgentToFakeProxy(t, fp, Config{})
	spawnID := spawnStubForUnaryProtocols(t, fp)
	t.Cleanup(func() { reapVia(t, fp, spawnID, 5) })

	const sentinelRole = "stubchild-reject"
	reqBytes, err := proto.Marshal(&genv1.ValidateRequest{Role: sentinelRole})
	require.NoError(t, err)

	resp := dispatchUnaryVia(t, fp, spawnID, protocolValidation, "Validate", reqBytes)
	require.Equal(t, genv1.DispatchFrame_DISPATCH_FRAME_KIND_DATA, resp.GetKind())

	var v genv1.ValidateResponse
	require.NoError(t, proto.Unmarshal(resp.GetPayload(), &v),
		"the response payload must decode as a ValidateResponse")
	require.False(t, v.GetValid(), "stub validator must REJECT the sentinel role")
	require.NotEmpty(t, v.GetErrors(), "rejecting validator must surface at least one finding")
	require.Equal(t, "stubchild_rejected", v.GetErrors()[0].GetClass(),
		"the rejecting finding must come from the real spawned stub validator")
}

// TestDispatchUnary_DataProcessing_BeginCommitFidelity routes two
// distinct DataProcessing RPCs (BeginCandidate then CommitCandidate)
// through dispatchUnaryByMethod and asserts the response of the second
// is deterministically derived from the response of the first. The
// stub's typed-data op:
//
//	BeginCandidate(idempotency_key=K) → CandidateHandle = "stub-candidate:K"
//	CommitCandidate(handle=H)         → CandidateMetadata = "stub-committed:H"
//
// so the second's metadata MUST be "stub-committed:stub-candidate:K".
// A routing slip that fired AbandonCandidate on the Commit call would
// return Empty (decoding the CommitCandidateResponse from an Empty
// gives empty bytes — the assertion catches it).
func TestDispatchUnary_DataProcessing_BeginCommitFidelity(t *testing.T) {
	fp := startFakeProxy(t)
	connectAgentToFakeProxy(t, fp, Config{})
	spawnID := spawnStubForUnaryProtocols(t, fp)
	t.Cleanup(func() { reapVia(t, fp, spawnID, 5) })

	const idemKey = "dp-idem-unit-1"
	beginBytes, err := proto.Marshal(&genv1.BeginCandidateRequest{
		ClaimHandleId:  "claim-handle-unit-1",
		IdempotencyKey: idemKey,
	})
	require.NoError(t, err)

	beginResp := dispatchUnaryVia(t, fp, spawnID, protocolDataProcessing, "BeginCandidate", beginBytes)
	require.Equal(t, genv1.DispatchFrame_DISPATCH_FRAME_KIND_DATA, beginResp.GetKind())

	var begin genv1.BeginCandidateResponse
	require.NoError(t, proto.Unmarshal(beginResp.GetPayload(), &begin),
		"the response must decode as a BeginCandidateResponse")
	wantHandle := []byte("stub-candidate:" + idemKey)
	require.True(t, bytes.Equal(begin.GetCandidateHandle(), wantHandle),
		"BeginCandidate handle %q did not come from the real spawned stub (got %q)",
		string(wantHandle), string(begin.GetCandidateHandle()))

	commitBytes, err := proto.Marshal(&genv1.CommitCandidateRequest{
		CandidateHandle: begin.GetCandidateHandle(),
	})
	require.NoError(t, err)

	commitResp := dispatchUnaryVia(t, fp, spawnID, protocolDataProcessing, "CommitCandidate", commitBytes)
	require.Equal(t, genv1.DispatchFrame_DISPATCH_FRAME_KIND_DATA, commitResp.GetKind())

	var commit genv1.CommitCandidateResponse
	require.NoError(t, proto.Unmarshal(commitResp.GetPayload(), &commit),
		"the response must decode as a CommitCandidateResponse")
	wantMetadata := append([]byte("stub-committed:"), wantHandle...)
	require.True(t, bytes.Equal(commit.GetCandidateMetadata(), wantMetadata),
		"CommitCandidate metadata %q did not deterministically derive from BeginCandidate's handle (got %q) — dispatchUnaryByMethod may have routed the call to the wrong DataProcessing RPC",
		string(wantMetadata), string(commit.GetCandidateMetadata()))
}

// TestDispatchUnary_UnknownRpcMethodCancels asserts the agent surfaces
// an unknown rpc_method on a known unary protocol as a CANCEL frame,
// not a DATA frame — the proxy translates CANCEL into the
// supervisor-facing error_class vocabulary, and a misrouted DATA from
// an unknown method would defeat that.
func TestDispatchUnary_UnknownRpcMethodCancels(t *testing.T) {
	fp := startFakeProxy(t)
	connectAgentToFakeProxy(t, fp, Config{})
	spawnID := spawnStubForUnaryProtocols(t, fp)
	t.Cleanup(func() { reapVia(t, fp, spawnID, 5) })

	resp := dispatchUnaryVia(t, fp, spawnID, protocolValidation, "NoSuchMethod", []byte("ignored"))
	require.Equal(t, genv1.DispatchFrame_DISPATCH_FRAME_KIND_CANCEL, resp.GetKind(),
		"an unknown rpc_method must cancel the dispatch — not pass through as a default response")
}
