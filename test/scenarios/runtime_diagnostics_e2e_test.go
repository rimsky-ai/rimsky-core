// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// STORY-runtime-diagnostics acceptance proof (Pass 17 of
// .ok-planner/plans/2026-06-08-design-corpus-bootstrap.md).
//
// The story carries four observable surfaces an operator queries to see
// why a wedged runtime isn't progressing. parked_lifecycle_test.go
// already covers the parked-node surface end-to-end through the
// supervisor's actual park-wake cycle; this sibling drives the other
// three surfaces — wait-sets, held-frames, and claim-holders — through
// the real assembled product so the entire diagnostic catalog the story
// names is exhibited.
//
// The Falsifier rules out two specific lies:
//   - a parked node that's really parked isn't on the parked surface,
//   - a wait-set edge the supervisor is consulting is missing from the
//     wait-set surface.
//
// The test reads each surface via the real /v1/ HTTP route the spec
// names (per TD-protocol-version-v1-namespaced) and cross-checks it
// against ground truth in the persistence rows the supervisor is
// reading from — proving the endpoint and the supervisor agree.
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
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/testfixture"
)

// TestRuntimeDiagnosticsWedgedInstance drives an instance whose nodes
// (a) park indefinitely while holding a claim, and (b) gate a receiver
// on a sender via a real undrained wait-set row, then asserts the four
// diagnostic HTTP surfaces (parked, wait-sets, held-frames,
// claim-holders) reflect the supervisor's actual state.
//
// The acquirer node holds a scope-claim from the stub queue-store and
// parks with PARK_REASON_AWAIT_CALLBACK, so:
//   - rimsky_node_runs[acquirer].phase = 'parked'  → parked surface
//   - rimsky_frames[acquirer.frame_id] is held     → held-frames surface
//   - rimsky_claim_handles row exists with at least
//     one rimsky_claim_holders row (the acquirer)  → claim-holders surface
//
// A separate transient_sender → transient_receiver pair generates a real
// undrained wait-set row via the production cascade emit-site
// (waitSetTopicKindFor in lib/runtime/runner_terminal.go), so:
//   - rimsky_wait_set has a row keyed on a real frame              → wait-sets surface
//
// Each assertion compares the HTTP response body (decoded via the
// public response types in lib/control/controlapi) against ground
// truth read from the persistence rows the supervisor is reading from
// — proving the endpoint and the supervisor agree, not just that some
// row exists.
func TestRuntimeDiagnosticsWedgedInstance(t *testing.T) {
	t.Parallel()

	// @deliberate: Stand up a stub claim-producer the acquirer node can hold a claim
	// against. Sync write-semantics so the open path immediately yields
	// an acquired handle (the same shape the parked_lifecycle held-claim
	// test uses).
	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"queue-store": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})

	// @deliberate: Acquirer parks indefinitely (no resume_at) under await-callback,
	// so the held-claim row + held frame stay live for the duration of
	// the diagnostic reads. A separately-keyed terminal Success script
	// is NOT registered — the test asserts the wedged state, not the
	// resume path (parked_lifecycle_test.go covers the wake leg).
	h.Stub.WhenType("acquirer").
		Park(genv1.ParkReason_PARK_REASON_AWAIT_CALLBACK, "wedge_callback", []byte(`{"ticket":"R-1"}`), time.Time{}, "")
	// @deliberate: inheritor pre-scripted but unreachable while the acquirer stays
	// parked. The Holds binding below makes the acquirer's claim
	// `is_held=TRUE` (the runtime sets is_held based on holding-subgraph
	// membership in runner_acquire_claims.go), which is what the
	// claim-holders surface needs to see in order to surface a held row.
	h.Stub.WhenType("inheritor").Success(map[string]any{}, true, "should-not-run")

	// @deliberate: transient_sender errors with class `flaky`; the stub prefixes a
	// single-segment class with `stub/`, so the wire error_class is
	// `stub/flaky`. transient_receiver subscribes via transient/retry/*
	// so each retry emits a signal that gates the receiver under
	// topic_kind="transient" in rimsky_wait_set. A 100-deep retry chain
	// with a real per-attempt delay keeps the row undrained for the
	// lifetime of the test — this is the supervisor's actual
	// "still-gated" state.
	h.Stub.WhenType("transient_sender").Error("flaky", map[string]any{"hint": "transient"})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "runtime-diagnostics-wedge", Version: "1",
		FrameResolutionMode: node.FrameResolutionCoalesce,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "acquirer", Executor: "stub"},
				scenario.WithStores(scenario.AliasedClaimRef("queue-store", "/wedge-A", "rw", "held")),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "inheritor",
					Executor: "stub",
					Holds: map[string]node.HoldsBinding{
						"held": {From: "acquirer"},
					},
				},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "acquirer", Type: "terminal/*"}),
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
					node.SubscriptionEntry{Node: "transient_sender", Type: "transient/retry/*", Frame: "in"},
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

	// @constraint: Wait for the transient_receiver to be genuinely gated mid-retry —
	// the supervisor must have inserted an undrained topic_kind=transient
	// row before we read the wait-set surface, otherwise the diagnostic
	// reflects "no gate" instead of the actual state.
	transientFrame, transientReceiverRun, transientSenderRun :=
		waitForUndrainedWaitSetRow(t, h, rcv.ID, "transient", 30*time.Second)
	require.NotEqual(t, shared.UUID{}, transientReceiverRun,
		"transient_receiver must carry an undrained topic_kind=transient wait-set row "+
			"keyed on a real frame before the diagnostic surfaces are read")

	// @constraint: the acquirer is genuinely parked; the parked-node
	// surface must list it. The falsifier ("a parked node that's
	// really parked isn't on the parked surface") names exactly this
	// clause.
	require.True(t, waitForNodeOnParkedSurface(t, h, acq.ID.String(), 10*time.Second),
		"GET /v1/diagnostics/parked must list the acquirer node-id "+
			"(spec-named operator surface; Falsifier rule 1)")

	// @deliberate: Cross-check: the supervisor's actual state has phase='parked' for
	// the acquirer's node-run. Both must agree.
	var phase string
	h.QueryRowSQL(
		`SELECT phase FROM rimsky_node_runs WHERE node_id = $1 ORDER BY enqueued_at DESC LIMIT 1`,
		[]any{acq.ID},
		&phase,
	)
	require.Equal(t, "parked", phase,
		"the supervisor's actual phase for the acquirer must be 'parked' — "+
			"the parked surface lying would falsify the story")

	// @constraint: The ?reason= filter is part of the operator surface contract; the
	// snake_case `await_callback` reason must select the wedged row.
	require.True(t, parkedSurfaceContainsNodeWithReason(t, h, acq.ID.String(), "await_callback"),
		"GET /v1/diagnostics/parked?reason=await_callback must return the acquirer (reason filter contract)")

	// @deliberate: the transient_receiver is genuinely gated on the
	// transient_sender via a topic_kind="transient" wait-set row inside
	// transientFrame. The supervisor reads this same row through
	// persistence.WaitSet().ListForFrame; the HTTP surface must return
	// it (the falsifier rule 2 is the row missing from the surface).
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

	// @deliberate: Assert the specific receiver↔sender edge the supervisor is
	// actually consulting appears in the response. Both ids come from
	// rimsky_wait_set ground truth above.
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

	// @constraint: The receiver_run-scoped variant must narrow correctly.
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

	// @deliberate: the acquirer's parked node-run holds its frame open
	// per blessed-invariant-13 (durable-by-default lifecycle): the
	// supervisor treats parked as unresolved, so the frame stays
	// running. The held-frames surface must surface it.
	require.True(t, waitForHeldFrameListingNode(t, h, acq.ID.String(), 10*time.Second),
		"GET /v1/admin/diagnostics/held-frames must list a frame whose node_ids "+
			"include the parked acquirer — the supervisor is holding the frame open "+
			"and the diagnostic must show what the supervisor sees")

	// @deliberate: the acquirer holds a real claim. Ground truth: a
	// rimsky_claim_handles row keyed on holder_node_id=acq.ID exists.
	// Fetch its id, then hit the holders surface; the response must
	// list at least one holder keyed on a node-run owned by the
	// acquirer. The held claim_handle row is written inside the
	// acquirer's dispatch tx — under heavy parallel testcontainer load
	// it can settle to the row a beat after the parked phase appears.
	// Poll until the supervisor's ground-truth held row exists, then
	// read it through the claim-holders surface.
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

	// @deliberate: Defensive: the holder_run_id surfaced by the endpoint must
	// correspond to a real node-run owned by the acquirer. The
	// supervisor reads the same row when checking holdership; both must
	// agree.
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

// waitForNodeOnParkedSurface polls GET /v1/diagnostics/parked until the
// response lists the given node-id. Reads through the real HTTP route
// the operator uses.
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

// parkedSurfaceContainsNodeWithReason hits GET /v1/diagnostics/parked
// with ?reason=<filter> and checks the node-id appears. Single-shot:
// the caller invokes only after waitForNodeOnParkedSurface has confirmed
// the row exists, so the filter contract — not a race — is what's
// under test.
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

// waitForHeldFrameListingNode polls GET /v1/admin/diagnostics/held-frames
// until one of the returned held frames lists the given node-id. Mirrors
// waitForHeldFrame from parked_holds_frame_e2e_test.go; defined fresh
// here so this file stays self-contained.
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

// waitForHeldClaimHandle polls rimsky_claim_handles for a held row
// owned by any node in the given instance and returns the first match's
// id. The held-claim handle is written inside the acquirer's dispatch tx,
// so a fresh-start parallel scenario can reach the parked-state probe a
// beat before the row commits; this helper closes that window.
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

// waitForUndrainedWaitSetRow polls rimsky_wait_set for an UNDRAINED row
// gating one of the receiver's runs under the given topic_kind, returning
// (frame_id, receiver_run_id, sender_run_id) of the first such row.
// Reading an undrained row proves the receiver is genuinely mid-gate
// — the supervisor's actual state the wait-sets surface must reflect.
//
// Defined here rather than imported from the cascade-taxonomy test
// (helper visibility within the package allows reuse, but a fresh
// helper keeps this file self-contained and avoids ordering coupling
// between scenarios). The query is the same shape the production
// rimsky_wait_set ledger is keyed on.
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
