// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package hostagent

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestSpawnRejectsUnsupportedLateBindProtocol(t *testing.T) {
	for _, protocol := range []string{"publisher", "validation", "data_processing"} {
		t.Run(protocol, func(t *testing.T) {
			bin := buildStubChild(t)
			fp := startFakeProxy(t)
			connectAgentToFakeProxy(t, fp, Config{})

			ack := spawnVia(t, fp, &genv1.Spawn{
				SpawnId:             uuid.NewString(),
				Binding:             &genv1.Binding{Path: bin},
				ExpectedProtocols:   []string{protocol},
				ReadyTimeoutSeconds: 5,
			})
			if ack.GetStatus() != genv1.SpawnAck_SPAWN_STATUS_FAILED {
				t.Fatalf("status = %v, want FAILED (the agent's sanctioned late-bind surface is executor and claim_producer only)", ack.GetStatus())
			}
			if ack.GetError().GetClass() != errClassSpawnFailed {
				t.Fatalf("error class = %q, want %q", ack.GetError().GetClass(), errClassSpawnFailed)
			}
			if !strings.Contains(ack.GetError().GetMessage(), protocol) {
				t.Fatalf("error message %q must name the rejected protocol %q", ack.GetError().GetMessage(), protocol)
			}
		})
	}
}

func TestDispatchFrameRejectsUnsupportedProtocol(t *testing.T) {
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
	t.Cleanup(func() { reapVia(t, fp, spawnID, 5) })

	streamID := uuid.NewString()
	fp.sendToAgent(t, &genv1.ServerFrame{Body: &genv1.ServerFrame_DispatchFrame{DispatchFrame: &genv1.DispatchFrame{
		SpawnId:   spawnID,
		Protocol:  "publisher",
		RpcMethod: "Subscribe",
		StreamId:  streamID,
		Kind:      genv1.DispatchFrame_DISPATCH_FRAME_KIND_DATA,
	}}})

	resp := nextDispatch(t, fp, streamID)
	if resp.GetKind() != genv1.DispatchFrame_DISPATCH_FRAME_KIND_CANCEL {
		t.Fatalf("kind = %v, want CANCEL (a dispatch for an unsupported late-bind protocol must be rejected, not silently routed to the spawned child)", resp.GetKind())
	}
}
