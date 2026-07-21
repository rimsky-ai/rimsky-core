// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package hostagent

import (
	"testing"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestDispatchClaimProducerCancelStopsBlockedOpen(t *testing.T) {
	bin := buildStubChild(t)
	t.Setenv("STUBCHILD_OPEN_BLOCK_UNTIL_CANCEL", "1")
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
	t.Cleanup(func() { reapVia(t, fp, spawnID, 5) })

	openBytes, _ := proto.Marshal(&genv1.OpenRequest{ClaimId: "claim-cancel-race", ProducerName: "fs", InstanceId: "inst-1"})
	streamID := uuid.NewString()
	fp.sendToAgent(t, &genv1.ServerFrame{Body: &genv1.ServerFrame_DispatchFrame{DispatchFrame: &genv1.DispatchFrame{
		SpawnId:           spawnID,
		Protocol:          protocolClaimProducer,
		Payload:           openBytes,
		StreamId:          streamID,
		Kind:              genv1.DispatchFrame_DISPATCH_FRAME_KIND_DATA,
		ClaimProducerVerb: genv1.DispatchFrame_CLAIM_PRODUCER_VERB_OPEN,
	}}})
	fp.sendToAgent(t, &genv1.ServerFrame{Body: &genv1.ServerFrame_DispatchFrame{DispatchFrame: &genv1.DispatchFrame{
		SpawnId:  spawnID,
		Protocol: protocolClaimProducer,
		StreamId: streamID,
		Kind:     genv1.DispatchFrame_DISPATCH_FRAME_KIND_CANCEL,
	}}})

	resp := nextDispatch(t, fp, streamID)
	if resp.GetKind() != genv1.DispatchFrame_DISPATCH_FRAME_KIND_CANCEL {
		t.Fatalf("kind = %v, want CANCEL — the child's Open blocks on ctx.Done() and only "+
			"returns once its dispatch context is actually cancelled; a DATA response here would "+
			"mean the claim-producer CANCEL frame never reached a registered cancel func",
			resp.GetKind())
	}
}

func TestDispatchExecutorCancelImmediatelyAfterDataIsNeverLost(t *testing.T) {
	bin := buildStubChild(t)
	t.Setenv("STUBCHILD_EXECUTE_BLOCK_UNTIL_CANCEL", "1")
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

	reqBytes, _ := proto.Marshal(&genv1.ExecuteRequest{NodeId: "node-cancel-race", InstanceId: "inst-1"})
	streamID := uuid.NewString()
	fp.sendToAgent(t, &genv1.ServerFrame{Body: &genv1.ServerFrame_DispatchFrame{DispatchFrame: &genv1.DispatchFrame{
		SpawnId:  spawnID,
		Protocol: protocolExecutor,
		Payload:  reqBytes,
		StreamId: streamID,
		Kind:     genv1.DispatchFrame_DISPATCH_FRAME_KIND_DATA,
	}}})
	fp.sendToAgent(t, &genv1.ServerFrame{Body: &genv1.ServerFrame_DispatchFrame{DispatchFrame: &genv1.DispatchFrame{
		SpawnId:  spawnID,
		Protocol: protocolExecutor,
		StreamId: streamID,
		Kind:     genv1.DispatchFrame_DISPATCH_FRAME_KIND_CANCEL,
	}}})

	resp := nextDispatch(t, fp, streamID)
	if resp.GetKind() != genv1.DispatchFrame_DISPATCH_FRAME_KIND_CANCEL {
		t.Fatalf("kind = %v, want CANCEL — the child's Execute blocks on ctx.Done() and only "+
			"returns once its dispatch context is actually cancelled; a DATA response here would "+
			"mean the CANCEL frame either errored out or never reached a registered cancel func",
			resp.GetKind())
	}

	reapVia(t, fp, spawnID, 5)
}
