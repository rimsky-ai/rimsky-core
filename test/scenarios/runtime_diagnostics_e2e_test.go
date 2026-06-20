// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/control/controlapi"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestRuntimeDiagnosticsWedgedInstance(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				"queue-store": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})

	h.Stub.WhenType("acquirer").
		Park(genv1.ParkReason_PARK_REASON_AWAIT_CALLBACK, "wedge_callback", time.Time{})
	h.Stub.WhenType("inheritor").Success(map[string]any{}, true, "should-not-run")

	h.Stub.WhenType("transient_sender").Error("flaky", map[string]any{"hint": "transient"})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "runtime-diagnostics-wedge", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "acquirer", Executor: "stub"},
				scenario.WithClaimProducers(scenario.AliasedClaimRef("queue-store", "/wedge-A", "rw", "held")),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "inheritor",
					Executor: "stub",
					Holds: map[string]node.HoldsBinding{
						"held": {From: "acquirer"},
					},
				},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "acquirer", Type: "terminal/*", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)}),
			),
			scenario.MakeNode(node.TemplateNodeDef{
				Type:     "transient_sender",
				Executor: "stub",
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"stub/flaky": {Policy: []node.PolicyAction{
						{Action: "retry", Count: 100, BaseDelayMs: 500},
					}},
				},
			}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "transient_receiver", Executor: "stub"},
				scenario.WithSubscribes(
					node.SubscriptionEntry{Node: "transient_sender", Type: "transient/retry/*", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)},
				),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-runtime-diag", map[string]any{})

	acq := h.FindNode(iid, "acquirer")
	rcv := h.FindNode(iid, "transient_receiver")
	require.NotNil(t, acq)
	require.NotNil(t, rcv)

	require.True(t, h.WaitForNodeState(acq.ID, cascade.NodeStateParked, 30*time.Second),
		"acquirer should reach parked under await-callback")

	transientFrame, transientReceiverRun, transientSenderRun :=
		waitForUndrainedWaitSetRow(t, h, rcv.ID, "transient", 30*time.Second)
	require.NotEqual(t, shared.UUID{}, transientReceiverRun,
		"transient_receiver must carry an undrained topic_kind=transient wait-set row "+
			"keyed on a real frame before the diagnostic surfaces are read")

	require.True(t, waitForNodeOnParkedSurface(t, h, acq.ID.String(), 10*time.Second),
		"GET /v1/diagnostics/parked must list the acquirer node-id "+
			"(spec-named operator surface; Falsifier rule 1)")

	var phase string
	h.QueryRowSQL(
		`SELECT phase FROM rimsky_node_runs WHERE node_id = $1 ORDER BY enqueued_at DESC LIMIT 1`,
		[]any{acq.ID},
		&phase,
	)
	require.Equal(t, "parked", phase,
		"the supervisor's actual phase for the acquirer must be 'parked' — "+
			"the parked surface lying would falsify the story")

	require.True(t, parkedSurfaceContainsNodeWithReason(t, h, acq.ID.String(), "await_callback"),
		"GET /v1/diagnostics/parked?reason=await_callback must return the acquirer (reason filter contract)")

	waitSetURL := h.ControlBase + "/v1/admin/diagnostics/wait-sets?frame=" + transientFrame.String()
	resp, err := http.Get(waitSetURL)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode,
		"wait-sets endpoint must return 200 with a valid ?frame= query param")
	var waitSetBody controlapi.WaitSetResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&waitSetBody))
	resp.Body.Close()
	require.NotEmpty(t, waitSetBody.WaitSet,
		"GET /v1/admin/diagnostics/wait-sets?frame=<frame> must list the supervisor's wait-set rows "+
			"(Falsifier rule 2: a wait-set edge the supervisor is consulting is missing from the surface)")

	foundEdge := false
	for _, e := range waitSetBody.WaitSet {
		if shared.UUID(e.ReceiverRunID) == transientReceiverRun &&
			shared.UUID(e.SenderRunID) == transientSenderRun &&
			e.TopicKind == "transient" {
			foundEdge = true
			break
		}
	}
	require.True(t, foundEdge,
		"the exact receiver↔sender edge the supervisor is consulting "+
			"(receiver_run=%s, sender_run=%s, topic_kind=transient) must appear on the wait-set surface",
		transientReceiverRun, transientSenderRun)

	resp2, err := http.Get(waitSetURL + "&receiver_run=" + transientReceiverRun.String())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp2.StatusCode,
		"wait-sets endpoint must accept ?receiver_run= as a narrowing filter")
	var narrowed controlapi.WaitSetResponse
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&narrowed))
	resp2.Body.Close()
	require.NotEmpty(t, narrowed.WaitSet,
		"the receiver_run-narrowed query must return the same edge — "+
			"empty would mean the supervisor's actual gate is hidden behind the narrow filter")
	for _, e := range narrowed.WaitSet {
		require.Equal(t, transientReceiverRun, shared.UUID(e.ReceiverRunID),
			"every narrowed row must key on the supplied receiver_run")
	}

	require.True(t, waitForHeldFrameListingNode(t, h, acq.ID.String(), 10*time.Second),
		"GET /v1/admin/diagnostics/held-frames must list a frame whose node_ids "+
			"include the parked acquirer — the supervisor is holding the frame open "+
			"and the diagnostic must show what the supervisor sees")

	claimHandleID := waitForHeldClaimHandle(t, h, iid, 10*time.Second)
	require.NotEqual(t, shared.UUID{}, claimHandleID,
		"the acquirer must hold a real rimsky_claim_handles row "+
			"before the claim-holders surface is read")

	holdersURL := h.ControlBase + "/v1/lock-holders/" + claimHandleID.String() + "/claim-holders"
	resp3, err := http.Get(holdersURL)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp3.StatusCode,
		"claim-holders endpoint must return 200 for a real claim_handle_id")
	var holdersBody struct {
		Holders []struct {
			ID            string `json:"id"`
			ClaimHandleID string `json:"claim_handle_id"`
			HolderRunID   string `json:"holder_run_id"`
			State         string `json:"state"`
		} `json:"holders"`
	}
	require.NoError(t, json.NewDecoder(resp3.Body).Decode(&holdersBody))
	resp3.Body.Close()
	require.NotEmpty(t, holdersBody.Holders,
		"GET /v1/lock-holders/{id}/claim-holders must list the holder(s) the supervisor is tracking; "+
			"an empty list would hide the wedge from the operator")
	holderClaimMatch := false
	for _, holder := range holdersBody.Holders {
		require.Equal(t, claimHandleID.String(), holder.ClaimHandleID,
			"every returned holder must key on the queried claim_handle_id")
		if holder.State == string(persistence.ClaimHolderStateActive) {
			holderClaimMatch = true
		}
	}
	require.True(t, holderClaimMatch,
		"at least one returned holder must be in state=active — the supervisor "+
			"is gripping this claim, the surface must reflect that")

	var acqRunCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_claim_holders ch
		   JOIN rimsky_node_runs r ON r.id = ch.holder_run_id
		  WHERE ch.claim_handle_id = $1 AND r.node_id = $2`,
		uuid.UUID(claimHandleID), uuid.UUID(acq.ID),
	).Scan(&acqRunCount))
	require.Greater(t, acqRunCount, 0,
		"the supervisor's ground-truth claim-holder row keys on the acquirer's node-run; "+
			"the HTTP surface must reflect this exact holder")
}

func waitForNodeOnParkedSurface(t *testing.T, h *scenario.Harness, nodeID string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(h.ControlBase + "/v1/diagnostics/parked")
		if err == nil {
			var body controlapi.ParkedNodesResponse
			decErr := json.NewDecoder(resp.Body).Decode(&body)
			resp.Body.Close()
			if decErr == nil {
				for _, p := range body.ParkedNodes {
					if p.NodeID == nodeID {
						return true
					}
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func parkedSurfaceContainsNodeWithReason(t *testing.T, h *scenario.Harness, nodeID, reason string) bool {
	t.Helper()
	resp, err := http.Get(h.ControlBase + "/v1/diagnostics/parked?reason=" + reason)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var body controlapi.ParkedNodesResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false
	}
	for _, p := range body.ParkedNodes {
		if p.NodeID == nodeID {
			return true
		}
	}
	return false
}

func waitForHeldFrameListingNode(t *testing.T, h *scenario.Harness, nodeID string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(h.ControlBase + "/v1/admin/diagnostics/held-frames")
		if err == nil {
			var body controlapi.HeldFramesResponse
			decErr := json.NewDecoder(resp.Body).Decode(&body)
			resp.Body.Close()
			if decErr == nil {
				for _, f := range body.Frames {
					for _, nid := range f.NodeIDs {
						if nid == nodeID {
							return true
						}
					}
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func waitForHeldClaimHandle(t *testing.T, h *scenario.Harness, instanceID shared.UUID, timeout time.Duration) shared.UUID {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var id shared.UUID
		err := h.Pool.QueryRow(h.Ctx,
			`SELECT lh.id FROM rimsky_claim_handles lh
			   JOIN rimsky_nodes n ON n.id = lh.holder_node_id
			  WHERE n.instance_id = $1 AND lh.is_held = TRUE
			  ORDER BY lh.claimed_at DESC LIMIT 1`,
			uuid.UUID(instanceID),
		).Scan(&id)
		if err == nil && id != (shared.UUID{}) {
			return id
		}
		time.Sleep(50 * time.Millisecond)
	}
	return shared.UUID{}
}

func waitForUndrainedWaitSetRow(
	t *testing.T, h *scenario.Harness, receiverNodeID shared.UUID,
	topicKind string, timeout time.Duration,
) (frameID, receiverRunID, senderRunID shared.UUID) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var (
			fid shared.UUID
			rid shared.UUID
			sid shared.UUID
			ok  bool
		)
		h.QuerySQL(`
            SELECT w.frame_id, w.receiver_run_id, w.sender_run_id
              FROM rimsky_wait_set w
              JOIN rimsky_node_runs r ON r.id = w.receiver_run_id
             WHERE r.node_id = $1
               AND w.topic_kind = $2
               AND w.drained_at IS NULL
             LIMIT 1
        `, []any{receiverNodeID, topicKind}, func(scan func(...any) error) error {
			if err := scan(&fid, &rid, &sid); err != nil {
				return err
			}
			ok = true
			return nil
		})
		if ok {
			return fid, rid, sid
		}
		time.Sleep(50 * time.Millisecond)
	}
	return shared.UUID{}, shared.UUID{}, shared.UUID{}
}
