// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// STORY-claim-handoff-across-frames scenario proof.
//
// Three variants pinning the spec's "held claim survives the frame
// boundary" acceptance: the holding subgraph's atomicity is governed
// by the subgraph's completion, NOT by any frame. Cross-frame
// co-holders see the claim still active at dispatch, resolve
// `{{claim.<alias>.address}}` against the same persisted bytes the
// acquirer received, and auto-terminal Commit fires only after the
// SLOWEST holder settles, even when the holders live in different
// frames.
//
//	V1 — frame: next per-node subscription. Acquirer + co-holder with
//	     Subscribes: [{ Node: "acquirer", Type: "terminal/success",
//	     Frame: "next" }]. The cascade walk opens a fresh frame for
//	     the co-holder. Asserts: distinct frame_ids on the two runs;
//	     claim_handle.state stays active across the boundary; only
//	     committed after the co-holder also settles.
//	V2 — instance: true cross-cutting subscription. Same shape but
//	     Subscribes: [{ Instance: true, Type: "terminal/success" }].
//	     Per concept:node-subscription, instance:true defaults
//	     Frame: "next". Same three properties asserted.
//	V3 — three-frame chain. Acquirer + two co-holders, each with
//	     Frame: "next" and Holds: against the upstream alias, each
//	     reading {{claim.X.address}}. Asserts: three distinct
//	     frame_ids; claim_handle stays active until the third frame's
//	     co-holder settles; substituted address bytes on each
//	     co-holder equal the acquirer's persisted Address bytes.
//
// Load-bearing property protected (per the plan's pass-2 Falsifier
// brief): cross-frame held-claim survival. The `claim_handle` row
// must stay `state = active` across frame boundaries until every
// holder in the subgraph settles. The proof verifies via the
// persistence layer (the row's `state` column + the runs' `frame_id`
// columns), NOT via slog frame.start/frame.end log lines — log lines
// are not persisted events and a test that relied on them would
// prove nothing about the row's actual lifecycle.
package scenarios

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/testfixture"
)

// TestClaimHandoff_AcrossFrames runs the three STORY-claim-handoff-
// across-frames variants. Each variant deploys its own template (the
// subscription shape differs per variant) and drives the holding
// subgraph end-to-end.
//
// @story: claim-handoff-across-frames
// @concept: claim-co-holdership
func TestClaimHandoff_AcrossFrames(t *testing.T) {
	t.Parallel()

	t.Run("V1_FrameNextPerNodeSubscription", testClaimHandoffAcrossFrames_FrameNextPerNode)
	t.Run("V2_InstanceTrueCrossCutting", testClaimHandoffAcrossFrames_InstanceTrue)
	t.Run("V3_ThreeFrameChain", testClaimHandoffAcrossFrames_ThreeFrameChain)
}

// testClaimHandoffAcrossFrames_FrameNextPerNode: an acquirer plus a
// co-holder whose Subscribes entry sets Frame: "next". The cascade walk
// opens a fresh frame for the co-holder; assert distinct frame_ids on
// the two runs, that the claim_handle row stays active across the
// boundary, and that auto-terminal Commit fires only after the
// co-holder also settles (a deliberate 2s stub Delay on the co-holder
// creates an observable gap so the active->committed transition can be
// asserted in two steps).
func testClaimHandoffAcrossFrames_FrameNextPerNode(t *testing.T) {

	h, acquirer, coHolder := startAcrossFramesHarness(t, acrossFramesOpts{
		// @deliberate: Per-node subscription with explicit Frame: "next" forces the
		// cascade walk to open a new frame for the co-holder.
		subscribes: []node.SubscriptionEntry{
			{Node: "acquirer", Type: "terminal/success", Frame: "next", WakeOnChange: spec.BoolPtr(true), ForceUpstreamRefresh: spec.BoolPtr(false)},
		},
		// @constraint: Delay the co-holder enough to observe the gap between the
		// acquirer's settlement and the co-holder's: while the co-holder
		// is in-flight in the new frame, claim_handle.state MUST stay
		// active. Auto-terminal Commit only fires when every holder is
		// non-active.
		coHolderDelay: 2 * time.Second,
	})

	require.True(t, h.WaitForNodeState(acquirer.ID, cascade.NodeStateFresh, 30*time.Second),
		"acquirer should settle fresh")

	// @constraint: At this point the co-holder is dispatched (the cascade walk
	// opened a fresh frame for it) but has not yet settled — its stub
	// is sleeping for 2s. The claim_handle row must remain active.
	// Wait until dispatch is observable (the run row exists), then
	// snapshot the row state — using the dispatch-wait keeps this
	// robust against scheduler tick latency without flaking on slow
	// CI.
	require.True(t, h.WaitForDispatch(coHolder.ID, 15*time.Second),
		"co-holder should be dispatched after the cascade walk opens the next frame")

	// @constraint: While the co-holder is in-flight, the claim handle MUST be
	// state=active. Cross-frame held-claim survival: the held claim's
	// lifetime is governed by the holding subgraph, not by the
	// acquirer's frame. Read the row inline (not the polling helper
	// requireClaimHandleState) — we are intentionally observing the
	// IN-FLIGHT state, not waiting for a transition.
	requireClaimHandleStateNow(t, h, acquirer.ID, spec.ClaimHandleStateActive, true,
		"while co-holder is in-flight in the next frame, claim_handle must remain active")

	// @deliberate: Now allow the co-holder to finish. After it settles, auto-
	// terminal Commit fires and the row promotes to committed.
	require.True(t, h.WaitForNodeState(coHolder.ID, cascade.NodeStateFresh, 30*time.Second),
		"co-holder should settle fresh after its 2s delay")
	requireClaimHandleState(t, h, acquirer.ID, spec.ClaimHandleStateCommitted, true)

	// @deliberate: Distinct frame_ids: the two runs live in different frames. This
	// is the load-bearing property "the held claim survives the frame
	// boundary" — if both runs shared a frame_id, there would BE no
	// boundary and the test would prove nothing.
	requireDistinctFrameIDs(t, h, []shared.UUID{acquirer.ID, coHolder.ID})
}

// testClaimHandoffAcrossFrames_InstanceTrue: cross-cutting subscription
// with Instance: true. Per concept:node-subscription, instance:true
// defaults Frame: "next" — the runtime opens a fresh frame for the
// co-holder. Same three properties as V1.
func testClaimHandoffAcrossFrames_InstanceTrue(t *testing.T) {

	h, acquirer, coHolder := startAcrossFramesHarness(t, acrossFramesOpts{
		// @deliberate: Cross-cutting subscription. No explicit Frame — runtime
		// defaults instance:true to "next" so the cascade walk opens a
		// new frame for the co-holder.
		subscribes: []node.SubscriptionEntry{
			{Instance: true, Type: "terminal/success", WakeOnChange: spec.BoolPtr(true), ForceUpstreamRefresh: spec.BoolPtr(false)},
		},
		coHolderDelay: 2 * time.Second,
	})

	require.True(t, h.WaitForNodeState(acquirer.ID, cascade.NodeStateFresh, 30*time.Second),
		"acquirer should settle fresh")

	require.True(t, h.WaitForDispatch(coHolder.ID, 15*time.Second),
		"co-holder should be dispatched after the cross-cutting cascade opens the next frame")

	requireClaimHandleStateNow(t, h, acquirer.ID, spec.ClaimHandleStateActive, true,
		"while co-holder is in-flight in the next frame, claim_handle must remain active")

	require.True(t, h.WaitForNodeState(coHolder.ID, cascade.NodeStateFresh, 30*time.Second),
		"co-holder should settle fresh after its 2s delay")
	requireClaimHandleState(t, h, acquirer.ID, spec.ClaimHandleStateCommitted, true)

	requireDistinctFrameIDs(t, h, []shared.UUID{acquirer.ID, coHolder.ID})
}

// testClaimHandoffAcrossFrames_ThreeFrameChain: acquirer + two
// co-holders. Each co-holder declares Holds: against the UPSTREAM and
// subscribes Frame: "next" so the chain spans three distinct frames:
//
//	frame F1: acquirer
//	frame F2: co-holder-1 (subscribes to acquirer's terminal/success)
//	frame F3: co-holder-2 (subscribes to co-holder-1's terminal/success)
//
// Asserts three distinct frame_ids, that the claim handle row stays
// state=active until the third frame's co-holder settles, and that the
// substituted address bytes on each co-holder equal the acquirer's
// persisted Address bytes (wire-payload parity in BOTH downstream
// frames, not just the first).
func testClaimHandoffAcrossFrames_ThreeFrameChain(t *testing.T) {

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{
			WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"queue-store": {
					Endpoint: "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{
						WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
					},
				},
			},
		},
	})
	h.Stub.WhenType("acquirer").Success(map[string]any{}, true, "acquired")
	h.Stub.WhenType("co-holder-1").Success(map[string]any{}, true, "co-held-1")
	// @deliberate: Delay co-holder-2 so the gap between co-holder-1's settlement and
	// co-holder-2's settlement is observable — assert claim_handle is
	// still active in F3 before the third holder finishes.
	h.Stub.WhenType("co-holder-2").Delay(2*time.Second).Success(map[string]any{}, true, "co-held-2")

	holdAttrs := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"held_addr": map[string]any{
				"type":   "string",
				"source": "{{claim.schema.address}}",
			},
		},
		"required": []any{"held_addr"},
	}

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "claim-handoff-three-frame-chain", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "acquirer", Executor: "stub"},
				scenario.WithStores(scenario.AliasedClaimRef("queue-store", "/three-frame-chain", "rw", "schema")),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "co-holder-1",
					Executor: "stub",
					Holds: map[string]node.HoldsBinding{
						"schema": {From: "acquirer"},
					},
				},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "acquirer", Type: "terminal/success", Frame: "next",
					WakeOnChange:         spec.BoolPtr(true),  // today-equivalent
					ForceUpstreamRefresh: spec.BoolPtr(false), // today-equivalent
				}),
				scenario.WithAttributes(holdAttrs),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "co-holder-2",
					Executor: "stub",
					Holds: map[string]node.HoldsBinding{
						// @constraint: Holds against the original acquirer (which is
						// the node that actually declares the
						// `schema` alias in its claims/stores block;
						// the validator's holds_unknown_claim_alias
						// rejection requires the `from:` target to
						// declare the alias). The chain shape lives
						// in the SUBSCRIBES wiring — co-holder-2
						// subscribes to co-holder-1's terminal/success
						// with Frame: "next", which opens F3.
						"schema": {From: "acquirer"},
					},
				},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "co-holder-1", Type: "terminal/success", Frame: "next",
					WakeOnChange:         spec.BoolPtr(true),  // today-equivalent
					ForceUpstreamRefresh: spec.BoolPtr(false), // today-equivalent
				}),
				scenario.WithAttributes(holdAttrs),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-claim-handoff-three-frame", map[string]any{})

	acquirer := h.FindNode(iid, "acquirer")
	holder1 := h.FindNode(iid, "co-holder-1")
	holder2 := h.FindNode(iid, "co-holder-2")
	require.NotNil(t, acquirer)
	require.NotNil(t, holder1)
	require.NotNil(t, holder2)

	require.True(t, h.WaitForNodeState(acquirer.ID, cascade.NodeStateFresh, 30*time.Second),
		"acquirer should settle fresh in F1")
	require.True(t, h.WaitForNodeState(holder1.ID, cascade.NodeStateFresh, 30*time.Second),
		"co-holder-1 should settle fresh in F2 (next frame after F1)")

	// @constraint: At this point F3 has been opened for co-holder-2 (the cascade
	// walked from co-holder-1's terminal/success into a fresh frame).
	// Co-holder-2's 2s Delay holds it in-flight; the held claim must
	// stay active across BOTH frame boundaries.
	require.True(t, h.WaitForDispatch(holder2.ID, 15*time.Second),
		"co-holder-2 should be dispatched in F3")

	requireClaimHandleStateNow(t, h, acquirer.ID, spec.ClaimHandleStateActive, true,
		"while co-holder-2 is in-flight in F3, claim_handle must remain active across both frame boundaries")

	require.True(t, h.WaitForNodeState(holder2.ID, cascade.NodeStateFresh, 30*time.Second),
		"co-holder-2 should settle fresh after its 2s delay")

	// @deliberate: Only after the THIRD frame's holder settles does Commit fire.
	requireClaimHandleState(t, h, acquirer.ID, spec.ClaimHandleStateCommitted, true)

	// @constraint: Three distinct frame_ids — one per run.
	requireDistinctFrameIDs(t, h, []shared.UUID{acquirer.ID, holder1.ID, holder2.ID})

	// @constraint: Wire-payload parity in BOTH downstream frames: the substituted
	// address bytes on EACH co-holder must equal the acquirer's
	// persisted Address bytes. Tests cross-frame substitution-context
	// resolution: the alias must resolve in F2 and in F3 to the same
	// bytes it would in F1.
	requireSubstitutedAddrMatchesAcquirer(t, h, acquirer.ID, holder1.ID,
		"co-holder-1's held_addr must equal acquirer's Address in F2")
	requireSubstitutedAddrMatchesAcquirer(t, h, acquirer.ID, holder2.ID,
		"co-holder-2's held_addr must equal acquirer's Address in F3")
}

type acrossFramesOpts struct {
	// subscribes overrides the co-holder's Subscribes block. The
	// variant chooses per-node + Frame: "next" or Instance: true.
	subscribes []node.SubscriptionEntry
	// coHolderDelay holds the co-holder in-flight long enough that the
	// gap between the acquirer's settlement and the co-holder's is
	// observable. Without a delay the runtime can race the
	// active->committed transition past the test's snapshot read.
	coHolderDelay time.Duration
}

// startAcrossFramesHarness boots a two-node holding-subgraph template
// (acquirer + co-holder reading `{{claim.schema.address}}`) and returns
// the harness plus the two node rows. Shared between V1 and V2 — they
// differ only in the co-holder's Subscribes shape.
func startAcrossFramesHarness(t *testing.T, opts acrossFramesOpts) (*scenario.Harness, *persistence.NodeRow, *persistence.NodeRow) {
	t.Helper()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{
			WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
		},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"queue-store": {
					Endpoint: "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{
						WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
					},
				},
			},
		},
	})
	h.Stub.WhenType("acquirer").Success(map[string]any{}, true, "acquired")
	coHolderBuilder := h.Stub.WhenType("co-holder")
	if opts.coHolderDelay > 0 {
		coHolderBuilder = coHolderBuilder.Delay(opts.coHolderDelay)
	}
	coHolderBuilder.Success(map[string]any{}, true, "co-held")

	holdAttrs := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"held_addr": map[string]any{
				"type":   "string",
				"source": "{{claim.schema.address}}",
			},
		},
		"required": []any{"held_addr"},
	}

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "claim-handoff-across-frames", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "acquirer", Executor: "stub"},
				scenario.WithStores(scenario.AliasedClaimRef("queue-store", "/across-frames", "rw", "schema")),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "co-holder",
					Executor: "stub",
					Holds: map[string]node.HoldsBinding{
						"schema": {From: "acquirer"},
					},
				},
				scenario.WithSubscribes(opts.subscribes...),
				scenario.WithAttributes(holdAttrs),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-claim-handoff-frames-"+shortRandSuffix(), map[string]any{})

	acquirer := h.FindNode(iid, "acquirer")
	coHolder := h.FindNode(iid, "co-holder")
	require.NotNil(t, acquirer)
	require.NotNil(t, coHolder)
	return h, acquirer, coHolder
}

// shortRandSuffix returns a short random suffix so parallel subtests
// don't collide on instance_key when the harness rejects duplicates.
func shortRandSuffix() string {
	return uuid.NewString()[:8]
}

// requireClaimHandleStateNow snapshots the claim_handle row right now
// and fails if state/is_held don't match. Distinct from
// requireClaimHandleState (the polling variant): this is for asserting
// the IN-FLIGHT state at a moment between two known events, not for
// waiting on an asynchronous transition.
func requireClaimHandleStateNow(t *testing.T, h *scenario.Harness, acquirerNodeID shared.UUID, wantState spec.ClaimHandleState, wantHeld bool, msg string) {
	t.Helper()
	row := readSingleClaimHandle(t, h, acquirerNodeID)
	require.Equalf(t, wantState, row.State, "%s; got state=%s is_held=%v", msg, row.State, row.IsHeld)
	require.Equalf(t, wantHeld, row.IsHeld, "%s; got state=%s is_held=%v", msg, row.State, row.IsHeld)
}

// requireDistinctFrameIDs asserts that every node in the input list
// has a frame_id (sourced from the node's latest run row) and that
// every frame_id is distinct. Used by all three variants to pin the
// "the held claim survives the frame boundary" property — if two
// holders shared a frame_id there would BE no frame boundary to cross.
func requireDistinctFrameIDs(t *testing.T, h *scenario.Harness, nodeIDs []shared.UUID) {
	t.Helper()
	seen := map[string]shared.UUID{}
	for _, nid := range nodeIDs {
		fid := latestRunFrameIDForNode(t, h, nid)
		fidStr := fid.String()
		if existing, ok := seen[fidStr]; ok {
			t.Fatalf("frame_id collision: node %s and node %s both ran in frame %s", existing, nid, fidStr)
		}
		seen[fidStr] = nid
	}
}

// latestRunFrameIDForNode returns the frame_id of the most-recent
// rimsky_node_runs row for the given node. The runtime persists
// frame_id on every dispatch (NOT NULL per invariant 19), so a node
// without a run row is a test bug — fail loudly.
func latestRunFrameIDForNode(t *testing.T, h *scenario.Harness, nodeID shared.UUID) shared.UUID {
	t.Helper()
	var fid uuid.UUID
	h.QueryRowSQL(`
        SELECT frame_id FROM rimsky_node_runs
         WHERE node_id = $1 ORDER BY enqueued_at DESC LIMIT 1
    `, []any{uuid.UUID(nodeID)}, &fid)
	require.NotEqual(t, uuid.Nil, fid, "node %s must have a run row with a frame_id", nodeID)
	return shared.UUID(fid)
}

// requireSubstitutedAddrMatchesAcquirer reads the acquirer's
// claim_handle.Address and the co-holder's substituted `held_addr`
// attribute inside one transaction, then asserts byte-equality (after
// re-encoding the substituted string as JSON to mirror the engine's
// stringifyRaw unwrap). Used by V3 to prove that cross-frame
// substitution resolves to the same bytes the acquirer received in
// every downstream frame.
func requireSubstitutedAddrMatchesAcquirer(t *testing.T, h *scenario.Harness, acquirerNodeID, coHolderNodeID shared.UUID, msg string) {
	t.Helper()
	coHolderRunID := latestRunIDForNode(t, h, coHolderNodeID)

	var acquirerAddr json.RawMessage
	var substituted any
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		rows, err := h.Persist.ClaimHandles().ListByHolderNode(h.Ctx, acquirerNodeID, tx)
		if err != nil {
			return err
		}
		require.Len(t, rows, 1, "exactly one claim_handle row should belong to the acquirer")
		handle, err := h.Persist.ClaimHandles().Get(h.Ctx, rows[0].ID, tx)
		if err != nil {
			return err
		}
		require.NotNil(t, handle)
		acquirerAddr = handle.Address

		attrs, err := h.Persist.NodeAttributes().GetByRun(h.Ctx, coHolderRunID, tx)
		if err != nil {
			return err
		}
		require.NotNil(t, attrs, "co-holder NodeAttributes row must exist after dispatch")
		substituted = attrs.Data["held_addr"]
		return nil
	}))

	gotStr, ok := substituted.(string)
	require.Truef(t, ok, "held_addr should land as a string after substitution; got %T", substituted)
	gotEncoded, err := json.Marshal(gotStr)
	require.NoError(t, err)
	require.Equalf(t, []byte(acquirerAddr), gotEncoded, "%s", msg)
}
