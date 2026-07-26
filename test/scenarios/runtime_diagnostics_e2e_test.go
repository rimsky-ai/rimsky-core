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
		Park(time.Now().Add(time.Hour))
	h.Stub.WhenType("signaler").Success(map[string]any{}, true, "signal-sent")
	h.Stub.WhenType("inheritor").Success(map[string]any{}, true, "should-not-run")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "runtime-diagnostics-wedge", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "acquirer", Executor: "stub"},
				scenario.WithClaimProducers(scenario.AliasedClaimRef("queue-store", "/wedge-A", "rw", "held")),
			),
			scenario.MakeNode(node.TemplateNodeDef{Type: "signaler", Executor: "stub"}),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "inheritor",
					Executor: "stub",
					Holds: map[string]node.HoldsBinding{
						"held": {From: "acquirer"},
					},
				},
				scenario.WithSubscribes(
					node.SubscriptionEntry{Node: "signaler", Type: "terminal/*", ForceUpstreamRefresh: node.BoolPtr(false)},
				),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-runtime-diag", map[string]any{})

	acq := h.FindNode(iid, "acquirer")
	rcv := h.FindNode(iid, "inheritor")
	require.NotNil(t, acq)
	require.NotNil(t, rcv)

	h.WaitForNodeState(acq.ID, cascade.NodeStateParked)

	wedgedFrame, wedgedReceiverRun, wedgedSenderRun :=
		waitForWaitSetRow(t, h, rcv.ID, "terminal")
	require.NotEqual(t, shared.UUID{}, wedgedReceiverRun,
		"inheritor must carry a topic_kind=terminal wait-set row from the settled signaler "+
			"keyed on a real frame before the diagnostic surfaces are read")

	waitForNodeOnParkedSurface(t, h, acq.ID.String())

	var phase string
	h.QueryRowSQL(
		`SELECT state FROM rimsky_node_runs WHERE node_id = $1 ORDER BY enqueued_at DESC LIMIT 1`,
		[]any{acq.ID},
		&phase,
	)
	require.Equal(t, "parked", phase,
		"the supervisor's actual state for the acquirer must be 'parked' — "+
			"the parked surface lying would falsify the story")

	waitSetURL := h.ControlBase + "/v1/admin/diagnostics/wait-sets?frame=" + wedgedFrame.String()
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
		if shared.UUID(e.ReceiverNodeRunID) == wedgedReceiverRun &&
			shared.UUID(e.SenderNodeRunID) == wedgedSenderRun &&
			e.TopicKind == "terminal" {
			foundEdge = true
			break
		}
	}
	require.True(t, foundEdge,
		"the exact receiver↔sender edge the supervisor is consulting "+
			"(receiver_run=%s, sender_run=%s, topic_kind=terminal) must appear on the wait-set surface",
		wedgedReceiverRun, wedgedSenderRun)

	resp2, err := http.Get(waitSetURL + "&receiver_run=" + wedgedReceiverRun.String())
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
		require.Equal(t, wedgedReceiverRun, shared.UUID(e.ReceiverNodeRunID),
			"every narrowed row must key on the supplied receiver_run")
	}

	waitForHeldFrameListingNode(t, h, acq.ID.String())

	claimHandleID := waitForHeldClaimHandle(t, h, iid)

	holdersURL := h.ControlBase + "/v1/claim-handles/" + claimHandleID.String() + "/holders"
	resp3, err := http.Get(holdersURL)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp3.StatusCode,
		"claim-holders endpoint must return 200 for a real claim_handle_id")
	var holdersBody struct {
		Holders []struct {
			ID              string `json:"id"`
			ClaimHandleID   string `json:"claim_handle_id"`
			HolderNodeRunID string `json:"holder_run_id"`
			State           string `json:"state"`
		} `json:"holders"`
	}
	require.NoError(t, json.NewDecoder(resp3.Body).Decode(&holdersBody))
	resp3.Body.Close()
	require.NotEmpty(t, holdersBody.Holders,
		"GET /v1/claim-handles/{id}/holders must list the holder(s) the supervisor is tracking; "+
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

	var inheritorState string
	for {
		require.NoError(t, h.Pool.QueryRow(h.Ctx,
			`SELECT state::text FROM rimsky_node_runs WHERE node_id = $1 ORDER BY enqueued_at DESC LIMIT 1`,
			uuid.UUID(rcv.ID),
		).Scan(&inheritorState))
		if inheritorState == string(cascade.NodeStateHeld) {
			break
		}
		require.Contains(t,
			[]string{string(cascade.NodeStateStale), string(cascade.NodeStateRunning), string(cascade.NodeStateHeld)},
			inheritorState,
			"the inheritor's run settles held behind the parked acquirer's held claim — stale (unclaimed), "+
				"running (mid-dispatch), and held are its only legal states; a fresh or failed terminal would "+
				"mean its settlement escaped the wedge")
		time.Sleep(50 * time.Millisecond)
	}

	var escapedTerminals int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_node_runs WHERE node_id = $1 AND state IN ($2, $3)`,
		uuid.UUID(rcv.ID), string(cascade.NodeStateFresh), string(cascade.NodeStateFailed),
	).Scan(&escapedTerminals))
	require.Equal(t, 0, escapedTerminals,
		"once the inheritor settles held, no run of it may carry a fresh or failed terminal — "+
			"a settled terminal would mean its settlement escaped the wedge")
	require.Equal(t, 1, h.EventCount(rcv.ID, "work_started"),
		"the inheritor dispatches exactly once on this path — one work_started event, never more")
	require.Equal(t, 1, h.DispatchCount(rcv.ID),
		"the inheritor's lineage holds exactly one run row, settled held behind the wedge")
}

func waitForNodeOnParkedSurface(t *testing.T, h *scenario.Harness, nodeID string) {
	t.Helper()
	for {
		resp, err := http.Get(h.ControlBase + "/v1/admin/diagnostics/parked-nodes")
		if err == nil {
			var body controlapi.ParkedNodesResponse
			decErr := json.NewDecoder(resp.Body).Decode(&body)
			resp.Body.Close()
			if decErr == nil {
				for _, p := range body.ParkedNodes {
					if p.NodeID == nodeID {
						return
					}
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func waitForHeldFrameListingNode(t *testing.T, h *scenario.Harness, nodeID string) {
	t.Helper()
	for {
		resp, err := http.Get(h.ControlBase + "/v1/admin/diagnostics/held-frames")
		if err == nil {
			var body controlapi.HeldFramesResponse
			decErr := json.NewDecoder(resp.Body).Decode(&body)
			resp.Body.Close()
			if decErr == nil {
				for _, f := range body.Frames {
					for _, nid := range f.NodeIDs {
						if nid == nodeID {
							return
						}
					}
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func waitForHeldClaimHandle(t *testing.T, h *scenario.Harness, instanceID shared.UUID) shared.UUID {
	t.Helper()
	for {
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
}

func waitForWaitSetRow(
	t *testing.T, h *scenario.Harness, receiverNodeID shared.UUID,
	topicKind string,
) (frameID, receiverNodeRunID, senderNodeRunID shared.UUID) {
	t.Helper()
	for {
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
}
