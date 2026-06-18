// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package hostagent

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

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
	if err != nil {
		t.Fatalf("marshal subscribe: %v", err)
	}

	resp := dispatchUnaryVia(t, fp, spawnID, protocolPublisher, "Subscribe", reqBytes)
	if resp.GetKind() != genv1.DispatchFrame_DISPATCH_FRAME_KIND_DATA {
		t.Fatalf("kind = %v, want DATA (no CANCEL on a misrouted RPC)", resp.GetKind())
	}

	var sub genv1.SubscribeResponse
	if err := proto.Unmarshal(resp.GetPayload(), &sub); err != nil {
		t.Fatalf("response payload must decode as SubscribeResponse: %v", err)
	}

	want := strings.Join([]string{subID, instanceID, targetNode}, " ")
	logBytes, err := os.ReadFile(publishLog)
	if err != nil {
		t.Fatalf("stub must have written publish log: %v", err)
	}
	if !strings.Contains(string(logBytes), want) {
		t.Fatalf("stub publisher did not record the Subscribe — dispatchUnaryByMethod misrouted; log=%q want substring %q",
			string(logBytes), want)
	}
}

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
	if err != nil {
		t.Fatalf("marshal unsubscribe: %v", err)
	}

	resp := dispatchUnaryVia(t, fp, spawnID, protocolPublisher, "Unsubscribe", reqBytes)
	if resp.GetKind() != genv1.DispatchFrame_DISPATCH_FRAME_KIND_DATA {
		t.Fatalf("kind = %v, want DATA", resp.GetKind())
	}

	var unsub genv1.UnsubscribeResponse
	if err := proto.Unmarshal(resp.GetPayload(), &unsub); err != nil {
		t.Fatalf("response must decode as UnsubscribeResponse: %v", err)
	}

	logBytes, _ := os.ReadFile(publishLog)
	if strings.Contains(string(logBytes), "pub-sub-unit-unsub") {
		t.Fatalf("stub recorded a Subscribe for an UnsubscribeRequest — dispatchUnaryByMethod misrouted; log=%q",
			string(logBytes))
	}
}

func TestDispatchUnary_Validation_RejectsSentinelRole(t *testing.T) {
	fp := startFakeProxy(t)
	connectAgentToFakeProxy(t, fp, Config{})
	spawnID := spawnStubForUnaryProtocols(t, fp)
	t.Cleanup(func() { reapVia(t, fp, spawnID, 5) })

	const sentinelRole = "stubchild-reject"
	reqBytes, err := proto.Marshal(&genv1.ValidateRequest{Role: sentinelRole})
	if err != nil {
		t.Fatalf("marshal validate: %v", err)
	}

	resp := dispatchUnaryVia(t, fp, spawnID, protocolValidation, "Validate", reqBytes)
	if resp.GetKind() != genv1.DispatchFrame_DISPATCH_FRAME_KIND_DATA {
		t.Fatalf("kind = %v, want DATA", resp.GetKind())
	}

	var v genv1.ValidateResponse
	if err := proto.Unmarshal(resp.GetPayload(), &v); err != nil {
		t.Fatalf("response must decode as ValidateResponse: %v", err)
	}
	if v.GetValid() {
		t.Fatal("stub validator must REJECT the sentinel role")
	}
	if len(v.GetErrors()) == 0 {
		t.Fatal("rejecting validator must surface at least one finding")
	}
	if got := v.GetErrors()[0].GetClass(); got != "stubchild_rejected" {
		t.Fatalf("rejecting finding class = %q, want %q", got, "stubchild_rejected")
	}
}

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
	if err != nil {
		t.Fatalf("marshal begin: %v", err)
	}

	beginResp := dispatchUnaryVia(t, fp, spawnID, protocolDataProcessing, "BeginCandidate", beginBytes)
	if beginResp.GetKind() != genv1.DispatchFrame_DISPATCH_FRAME_KIND_DATA {
		t.Fatalf("begin kind = %v, want DATA", beginResp.GetKind())
	}

	var begin genv1.BeginCandidateResponse
	if err := proto.Unmarshal(beginResp.GetPayload(), &begin); err != nil {
		t.Fatalf("response must decode as BeginCandidateResponse: %v", err)
	}
	wantHandle := []byte("stub-candidate:" + idemKey)
	if !bytes.Equal(begin.GetCandidateHandle(), wantHandle) {
		t.Fatalf("BeginCandidate handle %q did not come from the spawned stub (got %q)",
			string(wantHandle), string(begin.GetCandidateHandle()))
	}

	commitBytes, err := proto.Marshal(&genv1.CommitCandidateRequest{
		CandidateHandle: begin.GetCandidateHandle(),
	})
	if err != nil {
		t.Fatalf("marshal commit: %v", err)
	}

	commitResp := dispatchUnaryVia(t, fp, spawnID, protocolDataProcessing, "CommitCandidate", commitBytes)
	if commitResp.GetKind() != genv1.DispatchFrame_DISPATCH_FRAME_KIND_DATA {
		t.Fatalf("commit kind = %v, want DATA", commitResp.GetKind())
	}

	var commit genv1.CommitCandidateResponse
	if err := proto.Unmarshal(commitResp.GetPayload(), &commit); err != nil {
		t.Fatalf("response must decode as CommitCandidateResponse: %v", err)
	}
	wantMetadata := append([]byte("stub-committed:"), wantHandle...)
	if !bytes.Equal(commit.GetCandidateMetadata(), wantMetadata) {
		t.Fatalf("CommitCandidate metadata %q did not derive from BeginCandidate's handle (got %q) — possible misrouted RPC",
			string(wantMetadata), string(commit.GetCandidateMetadata()))
	}
}

func TestDispatchUnary_UnknownRpcMethodCancels(t *testing.T) {
	fp := startFakeProxy(t)
	connectAgentToFakeProxy(t, fp, Config{})
	spawnID := spawnStubForUnaryProtocols(t, fp)
	t.Cleanup(func() { reapVia(t, fp, spawnID, 5) })

	resp := dispatchUnaryVia(t, fp, spawnID, protocolValidation, "NoSuchMethod", []byte("ignored"))
	if resp.GetKind() != genv1.DispatchFrame_DISPATCH_FRAME_KIND_CANCEL {
		t.Fatalf("kind = %v, want CANCEL (unknown rpc_method must cancel, not pass through)", resp.GetKind())
	}
}
