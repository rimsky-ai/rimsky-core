// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// STORY-claim-handoff-durable scenario proof.
//
// Five subcases pinning the spec's "durable held claim survives across
// instance dispatches" acceptance: a `lifetime: durable` claim handle
// row persists past the producing dispatch's terminal AND past a forced
// retention-sweep tick, future dispatches can `holds:` the same upstream
// alias and read the persisted bytes, the row participates in conflict
// detection while committed-durable, and it leaves the active-scope set
// only through the asset Release path or the instance-termination
// held-durable-release path.
//
//	A — Cross-dispatch persistence: open the durable claim, drive to
//	    fresh, force runtime.SweepClaimHandleRetention, re-read the row
//	    through real persistence — still present, state=committed,
//	    lifetime=durable. Pins the @blessed-invariant 22 carve-out for
//	    durable-committed rows in the retention sweep predicate.
//	B — Cross-dispatch `holds:`: from the same instance, re-invalidate
//	    the co-holder so it dispatches a SECOND time. The substituted
//	    {{claim.<alias>.address}} bytes equal the persisted Address on
//	    the durable claim_handle row from D1. The co-holder settles
//	    fresh — the substitution context's `claim.<alias>.address`
//	    resolved through the post-Pass-1 merge of acq.HeldClaims at
//	    dispatch.
//	C — Conflict detection includes committed-durable: a SECOND template
//	    on a SECOND instance whose acquirer Opens the SAME scope against
//	    the SAME producer must settle terminal/error/acquire/unavailable
//	    while the durable row is committed-durable on the first instance.
//	    Pins concept:claim-lifetime invariant "Conflict detection includes
//	    committed-durable rows" (the asset surface still occupies the
//	    scope).
//	D — Asset Release path: DELETE /v1/instances/{id}/assets/{alias}
//	    removes the durable row from the active-scope set; the
//	    conflicting acquirer from Subcase C, re-invalidated, now settles
//	    fresh.
//	E — Instance-termination release: a fresh durable-acquirer instance,
//	    driven to committed-durable, then taken through POST /terminate
//	    → DELETE /v1/instances/{id}. The DELETE handler is the sole
//	    caller of runtime.ReleaseHeldDurableClaims; after the DELETE
//	    returns 200 the claim_handle row is gone, the instance row is
//	    gone.
//
// Load-bearing property protected (per the plan's pass-3 Falsifier
// brief): cross-dispatch durable persistence — the retention sweep tick
// MUST be forced in subcase A (sweep skipping is the durable-exemption
// invariant; a test that doesn't force a sweep proves nothing). Reads
// go through the real persistence-layer state (`h.Persist.ClaimHandles`
// rows + the control-API DELETE), never via in-memory return values.
package scenarios

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/testfixture"
)

// TestClaimHandoff_Durable runs the five STORY-claim-handoff-durable
// subcases. Each subcase boots its own harness + stub producer so per-
// subcase scope conflicts don't leak across (the conflict subcase needs
// a fresh harness alongside the producer's committed-durable row from
// the first half of the same test).
//
// @story: claim-handoff-durable
// @concept: claim-lifetime
// @concept: claim-handle
func TestClaimHandoff_Durable(t *testing.T) {
	t.Parallel()

	t.Run("A_CrossDispatchPersistence", testClaimHandoffDurable_CrossDispatchPersistence)
	t.Run("B_CrossDispatchHolds", testClaimHandoffDurable_CrossDispatchHolds)
	t.Run("C_ConflictDetectionIncludesCommittedDurable", testClaimHandoffDurable_ConflictDetection)
	t.Run("D_AssetReleasePath", testClaimHandoffDurable_AssetRelease)
	t.Run("E_InstanceTerminationRelease", testClaimHandoffDurable_InstanceTerminationRelease)
}

// testClaimHandoffDurable_CrossDispatchPersistence — Subcase A.
//
// Deploy a durable-acquirer template, drive the acquirer to fresh in
// dispatch D1, force a retention-sweep tick directly, then re-read the
// claim_handle row via persistence. The row MUST still exist at
// state=committed with lifetime=durable — that's the @blessed-invariant
// 22 carve-out the sweep predicate enforces.
//
// Load-bearing: forcing the sweep is the heart of the proof. The
// existing acquirer-driven path may itself leave the row present
// momentarily; the sweep tick is what proves the row survives the
// reaper's predicate, not just incidental timing.
func testClaimHandoffDurable_CrossDispatchPersistence(t *testing.T) {

	h, acquirer, _ := startDurableHarness(t, durableOpts{
		instanceKey: "ck-claim-handoff-durable-A",
		selector:    "/durable-A",
	})

	require.True(t, h.WaitForNodeState(acquirer.ID, cascade.NodeStateFresh, 30*time.Second),
		"acquirer should settle fresh in D1")

	// @constraint: Wait for the durable Promote: auto-terminal fires after the
	// holding subgraph completes; the row transitions from active to
	// committed (Promote-not-Delete on durable per @blessed-invariant
	// 22). Without the wait the row may still read state=active.
	requireDurableCommittedHandle(t, h, acquirer.ID)

	// @constraint: Force the retention sweep tick directly. The sweep MUST be a
	// no-op against a committed-durable row — that's the carve-out.
	// Use a long trailing window so any non-durable-committed row that
	// happened to be present would also survive (we want the
	// invariant assertion to depend on lifetime/state, not on the
	// cutoff math).
	cfg := runtime.RetentionConfig{ClaimHandlesTrailing: 30 * 24 * time.Hour}
	n, err := runtime.SweepClaimHandleRetention(h.Ctx, h.Persist.ClaimHandles(), cfg,
		time.Now(), shared.SilentLogger{})
	require.NoError(t, err)
	require.Equal(t, 0, n,
		"retention sweep MUST NOT reap a committed-durable row (@blessed-invariant 22)")

	// @constraint: Tighter sweep: even with a zero-day trailing window the sweep
	// must STILL preserve committed-durable rows (the lifetime+state
	// predicate beats the time predicate for durable).
	cfgAggressive := runtime.RetentionConfig{ClaimHandlesTrailing: 1 * time.Nanosecond}
	n2, err := runtime.SweepClaimHandleRetention(h.Ctx, h.Persist.ClaimHandles(), cfgAggressive,
		time.Now(), shared.SilentLogger{})
	require.NoError(t, err)
	require.Equal(t, 0, n2,
		"aggressive retention sweep MUST NOT reap a committed-durable row")

	// @constraint: Re-read via persistence and confirm still committed + durable.
	row := readDurableClaimHandle(t, h, acquirer.ID)
	require.NotNil(t, row, "durable claim_handle row must survive the sweep")
	require.Equal(t, spec.ClaimHandleStateCommitted, row.State)
	require.Equal(t, spec.ClaimLifetimeDurable, row.Lifetime)
}

// testClaimHandoffDurable_CrossDispatchHolds — Subcase B.
//
// Single instance. Acquirer Opens a durable claim; co-holder reads
// {{claim.<alias>.address}} via its attribute schema. After both settle
// fresh in D1, re-invalidate the co-holder so it dispatches a SECOND
// time (D2). On D2 the co-holder's substitution MUST resolve to bytes
// equal to the persisted durable claim_handle.Address — the holds
// binding walks to the still-present durable row from D1.
//
// This pins the spec's "future dispatches in the same instance can
// co-hold the same durable row by alias" property in the same-instance
// re-dispatch flavor: the substitution context's `claim.<alias>.address`
// resolves through acq.HeldClaims at dispatch time (Pass-1's
// runner_dispatch merge), sourced from the durable row whose Promote-
// at-terminal kept it on the table.
func testClaimHandoffDurable_CrossDispatchHolds(t *testing.T) {

	h, acquirer, coHolder := startDurableHandoffHarness(t, durableHandoffOpts{
		instanceKey:  "ck-claim-handoff-durable-B",
		selector:     "/durable-B",
		alias:        "asset",
		coHolderType: "co-holder",
		coHolderAttrs: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"held_addr": map[string]any{"type": "string", "source": "{{claim.asset.address}}"},
			},
			"required": []any{"held_addr"},
		},
	})

	require.True(t, h.WaitForNodeState(acquirer.ID, cascade.NodeStateFresh, 30*time.Second),
		"acquirer should settle fresh in D1")
	require.True(t, h.WaitForNodeState(coHolder.ID, cascade.NodeStateFresh, 30*time.Second),
		"co-holder should settle fresh in D1 (substitution resolves on the live held claim)")

	requireDurableCommittedHandle(t, h, acquirer.ID)

	// @deliberate: Capture the persisted address bytes — these are the bytes we
	// expect the co-holder's D2 substitution to equal.
	d1Handle := readDurableClaimHandle(t, h, acquirer.ID)
	require.NotNil(t, d1Handle)
	d1Address := append(json.RawMessage(nil), d1Handle.Address...)

	// @deliberate: Capture the D1 run id so we can pick up the D2 run row that
	// will appear after invalidate.
	d1RunID := latestRunIDForNode(t, h, coHolder.ID)

	// @deliberate: D2: re-invalidate the co-holder via a per-target
	// typed-message wake. On D2 acquire-tx, loadInheritedClaimsForNode
	// walks holds:asset → acquirer's durable claim_handle row (still
	// present at state=committed) → fills acq.HeldClaims;
	// buildResolveContextForDispatch (post Pass-1) merges acq.HeldClaims
	// into the substitution context so `{{claim.asset.address}}`
	// resolves.
	h.PostInstanceMessage(coHolder.InstanceID,
		"test/wake/"+coHolder.NodeType, nil,
		fmt.Sprintf("test-wake-%s-1", t.Name()))

	// @deliberate: Wait until the co-holder has a NEW run row distinct from the
	// D1 run id, then wait for it to settle fresh.
	requireSecondRun(t, h, coHolder.ID, d1RunID, 30*time.Second)
	require.True(t, h.WaitForNodeState(coHolder.ID, cascade.NodeStateFresh, 30*time.Second),
		"co-holder should settle fresh again on D2 (no terminal/error/template_resolution_failed)")

	// @constraint: Read the D2 substituted bytes through real persistence and
	// compare byte-for-byte with the D1-captured Address.
	d2RunID := latestRunIDForNode(t, h, coHolder.ID)
	require.NotEqual(t, d1RunID, d2RunID,
		"D2 run id must differ from D1 — the invalidate must have produced a fresh run")
	requireSubstitutedAddrEquals(t, h, d2RunID, "held_addr", d1Address,
		"D2 co-holder's substituted {{claim.asset.address}} bytes must equal D1 persisted durable Address")
}

// testClaimHandoffDurable_ConflictDetection — Subcase C.
//
// A first instance opens a durable claim against the producer at a
// specific scope. A second instance (separate template, same producer
// + same selector) attempts to Open the SAME scope while the first
// instance's row is committed-durable. The second acquirer MUST settle
// terminal/error/acquire/unavailable — committed-durable rows
// participate in conflict detection (the asset surface still occupies
// the scope).
//
// Pins concept:claim-lifetime invariant "Conflict detection includes
// committed-durable rows."
func testClaimHandoffDurable_ConflictDetection(t *testing.T) {

	// @deliberate: Boot a single harness; both instances share the same producer
	// process so the scope conflict is real (the producer's claim-scope
	// guard is what surfaces unavailability to rimsky).
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
	h.Stub.WhenType("durable-acquirer").Success(map[string]any{}, true, "owned")
	h.Stub.WhenType("competing-acquirer").Success(map[string]any{}, true, "should-not-run")

	const sharedSelector = "/durable-C-shared"

	// @deliberate: First template + instance: opens the durable claim on the
	// contested scope. Drive to committed-durable.
	durableTid := h.DeployTemplate(node.TemplateSpec{
		Name: "claim-handoff-durable-C-owner", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "durable-acquirer", Executor: "stub"},
				scenario.WithStores(node.NodeStoreRef{
					Name:     "queue-store",
					Selector: sharedSelector,
					Intent:   "rw",
					Alias:    "asset",
					Lifetime: string(spec.ClaimLifetimeDurable),
				}),
			),
		},
	})
	durableIID := h.CreateInstance(durableTid, "ck-claim-handoff-durable-C-owner", map[string]any{})
	durableAcq := h.FindNode(durableIID, "durable-acquirer")
	require.NotNil(t, durableAcq)
	require.True(t, h.WaitForNodeState(durableAcq.ID, cascade.NodeStateFresh, 30*time.Second),
		"durable acquirer should settle fresh and Promote its claim to committed-durable")
	requireDurableCommittedHandle(t, h, durableAcq.ID)

	// @deliberate: Second template: a different node type, same store + same
	// selector, declaring error_types: {acquire/unavailable: [give_up]}
	// so the produced terminal/error/acquire/unavailable lands as a
	// failed-color settlement we can assert.
	competingTid := h.DeployTemplate(node.TemplateSpec{
		Name: "claim-handoff-durable-C-competitor", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "competing-acquirer",
					Executor: "stub",
					ErrorTypes: map[string]node.ErrorTypePolicy{
						"acquire/unavailable": {
							Policy: []node.PolicyAction{{Action: "give_up"}},
						},
					},
				},
				scenario.WithStores(node.NodeStoreRef{
					Name:     "queue-store",
					Selector: sharedSelector,
					Intent:   "rw",
					Alias:    "asset",
				}),
			),
		},
	})
	competingIID := h.CreateInstance(competingTid, "ck-claim-handoff-durable-C-competitor", map[string]any{})
	competingAcq := h.FindNode(competingIID, "competing-acquirer")
	require.NotNil(t, competingAcq)

	// @constraint: The competing acquirer must settle failed — the durable row's
	// participation in conflict detection denies the Open.
	if !h.WaitForNodeState(competingAcq.ID, cascade.NodeStateFailed, 30*time.Second) {
		// @deliberate: Diagnostic: surface the competitor's current state + the
		// durable row's lifetime/state so a failing run names what the
		// conflict detection saw.
		var compRow *persistence.NodeRow
		var owners []persistence.ClaimHandleRow
		require.NoError(t, h.InTx(func(tx persistence.Tx) error {
			r, err := h.Persist.Nodes().Get(h.Ctx, competingAcq.ID, tx)
			if err != nil {
				return err
			}
			compRow = r
			rs, err := h.Persist.ClaimHandles().ListByProducerClaimScope(h.Ctx, "queue-store", tx)
			if err != nil {
				return err
			}
			owners = rs
			return nil
		}))
		owSummary := make([]string, len(owners))
		for i, ow := range owners {
			owSummary[i] = string(ow.State) + "/" + string(ow.Lifetime) +
				" scope=" + string(ow.ClaimScopeData)
		}
		t.Fatalf("competing acquirer must settle failed via acquire/unavailable while durable row occupies the scope; "+
			"got state=%s settling_signal_type=%v; conflict-set holders=%v",
			compRow.State, compRow.SettlingSignalType, owSummary)
	}

	// @constraint: And the settling signal must carry the canonical
	// terminal/error/acquire/unavailable shape per concept:signal.
	requireSettlingSignalTypePrefix(t, h, competingAcq.ID, "terminal/error/acquire/unavailable")

	// @constraint: The competing acquirer's executor MUST NOT have been invoked.
	for _, obs := range h.Stub.Observed() {
		require.NotEqual(t, "competing-acquirer", obs.NodeType,
			"competing-acquirer must not be dispatched to the executor when acquire/unavailable fires")
	}
}

// testClaimHandoffDurable_AssetRelease — Subcase D.
//
// First instance opens a durable claim and Promotes to committed-
// durable. A SECOND instance whose acquirer competes on the SAME scope
// initially settles terminal/error/acquire/unavailable (as in Subcase
// C). Then DELETE /v1/instances/{id}/assets/{alias} removes the
// durable row from the active-scope set. A THIRD instance — a fresh
// competitor against the same scope — now settles FRESH (the scope is
// free).
//
// We use a fresh third instance rather than re-invalidating the failed
// second-instance node because the operator-driven invalidate path on
// an already-failed node enqueues a frame but does not re-dispatch a
// failed-terminal node without a fresh re-acquisition cycle. A fresh
// third instance is the cleanest model for "a subsequent acquirer
// against the same scope succeeds" — exactly what the spec's
// Acceptance names.
//
// Load-bearing: the asset DELETE handler is the sanctioned operator
// release path. After DELETE returns 200 the contested scope is
// available again — that's the proof.
func testClaimHandoffDurable_AssetRelease(t *testing.T) {

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
	h.Stub.WhenType("durable-acquirer").Success(map[string]any{}, true, "owned")
	h.Stub.WhenType("competing-acquirer").Success(map[string]any{}, true, "won")

	const sharedSelector = "/durable-D-shared"

	// @deliberate: Durable owner template — single durable-acquirer node.
	durableTid := h.DeployTemplate(node.TemplateSpec{
		Name: "claim-handoff-durable-D-owner", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "durable-acquirer", Executor: "stub"},
				scenario.WithStores(node.NodeStoreRef{
					Name:     "queue-store",
					Selector: sharedSelector,
					Intent:   "rw",
					Alias:    "asset",
					Lifetime: string(spec.ClaimLifetimeDurable),
				}),
			),
		},
	})
	durableIID := h.CreateInstance(durableTid, "ck-claim-handoff-durable-D-owner", map[string]any{})
	durableAcq := h.FindNode(durableIID, "durable-acquirer")
	require.NotNil(t, durableAcq)
	require.True(t, h.WaitForNodeState(durableAcq.ID, cascade.NodeStateFresh, 30*time.Second))
	requireDurableCommittedHandle(t, h, durableAcq.ID)

	// @deliberate: Competing template — give_up on unavailable so we can re-trigger
	// it via /invalidate after the durable row releases.
	competingTid := h.DeployTemplate(node.TemplateSpec{
		Name: "claim-handoff-durable-D-competitor", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "competing-acquirer",
					Executor: "stub",
					ErrorTypes: map[string]node.ErrorTypePolicy{
						"acquire/unavailable": {
							Policy: []node.PolicyAction{{Action: "give_up"}},
						},
					},
				},
				scenario.WithStores(node.NodeStoreRef{
					Name:     "queue-store",
					Selector: sharedSelector,
					Intent:   "rw",
					Alias:    "asset",
				}),
			),
		},
	})
	competingIID := h.CreateInstance(competingTid, "ck-claim-handoff-durable-D-competitor", map[string]any{})
	competingAcq := h.FindNode(competingIID, "competing-acquirer")
	require.NotNil(t, competingAcq)

	// @constraint: First competing attempt: must fail (the durable owner has the
	// scope).
	require.True(t, h.WaitForNodeState(competingAcq.ID, cascade.NodeStateFailed, 30*time.Second),
		"competing acquirer must initially fail with acquire/unavailable")

	// @deliberate: Operator releases the durable claim through the sanctioned asset
	// delete path. The asset alias is the dotted `{node_type}.{alias}`
	// form per code:assets.go::parseAssetAlias.
	deleteAsset(t, h, durableIID, "durable-acquirer.asset")

	// @deliberate: Confirm the durable row is gone from the active-scope set —
	// `Get` returns nil.
	requireClaimHandleAbsent(t, h, durableAcq.ID)

	// @deliberate: Create a THIRD instance — a fresh subsequent acquirer against the
	// same scope. With the durable row gone, the producer's scope is
	// free; the Open succeeds and this competitor settles fresh.
	thirdIID := h.CreateInstance(competingTid, "ck-claim-handoff-durable-D-third", map[string]any{})
	thirdAcq := h.FindNode(thirdIID, "competing-acquirer")
	require.NotNil(t, thirdAcq)

	if !h.WaitForNodeState(thirdAcq.ID, cascade.NodeStateFresh, 30*time.Second) {
		var thirdRow *persistence.NodeRow
		require.NoError(t, h.InTx(func(tx persistence.Tx) error {
			r, err := h.Persist.Nodes().Get(h.Ctx, thirdAcq.ID, tx)
			thirdRow = r
			return err
		}))
		t.Fatalf("fresh subsequent acquirer must settle fresh after the durable owner's asset is released; "+
			"got state=%s settling_signal_type=%v", thirdRow.State, thirdRow.SettlingSignalType)
	}
}

// testClaimHandoffDurable_InstanceTerminationRelease — Subcase E.
//
// Fresh instance, durable-acquirer settles fresh + Promotes to
// committed-durable. The operator-driven release path runs in two HTTP
// steps:
//
//  1. POST /v1/instances/{id}/terminate — sets terminated_at on the
//     instance, satisfying DELETE's terminal-state precondition.
//  2. DELETE /v1/instances/{id} — handleDeleteInstance is the sole
//     caller of runtime.ReleaseHeldDurableClaims; it fires producer
//     Release on every durable row, deletes the rows, then deletes the
//     instance row.
//
// After DELETE returns 200: the claim_handle row is gone, the instance
// row is gone (handleDeleteInstance::instances.go:839 removes the row
// entirely).
func testClaimHandoffDurable_InstanceTerminationRelease(t *testing.T) {

	h, acquirer, stub := startDurableHarness(t, durableOpts{
		instanceKey: "ck-claim-handoff-durable-E",
		selector:    "/durable-E",
	})

	require.True(t, h.WaitForNodeState(acquirer.ID, cascade.NodeStateFresh, 30*time.Second),
		"acquirer should settle fresh and Promote its durable claim")
	requireDurableCommittedHandle(t, h, acquirer.ID)

	// @deliberate: Capture the claim_handle id so we can read it back through Get
	// after the DELETE.
	durableHandleID := readDurableClaimHandle(t, h, acquirer.ID).ID
	durableClaimID := durableHandleID.String()
	instanceID := acquirer.InstanceID

	// @deliberate: Step 1: POST /terminate sets terminated_at on the row. The
	// terminate handler force-fails any in-flight runs and marks the
	// instance terminal; required precondition for DELETE.
	terminateInstance(t, h, instanceID)
	require.True(t, waitForInstanceTerminatedDurable(t, h, instanceID, 15*time.Second),
		"POST /terminate must set terminated_at on the instance row")

	// @deliberate: Step 2: DELETE /v1/instances/{id}. This is the sole caller of
	// runtime.ReleaseHeldDurableClaims (per the comment block at
	// instances.go:804). Producer Release fires, claim_handle row is
	// deleted, instance row is deleted.
	deleteInstance(t, h, instanceID)

	requireClaimHandleGoneByID(t, h, durableHandleID)

	requireInstanceGone(t, h, instanceID)

	// @constraint: And the producer recorded a Release call against the durable
	// claim_id — the held-durable-release path inside
	// runtime.ReleaseHeldDurableClaims fired. The Falsifier names
	// "instance termination doesn't fire the held-durable-release
	// path" as one of the failure modes this proof must close, so
	// we drive the producer-side observable directly via
	// stub.Calls() rather than only asserting the rimsky-side row
	// deletion.
	requireProducerRelease(t, stub, durableClaimID)
}

// requireProducerRelease asserts that the stub store recorded a
// `release` verb against the given claim_id. The stub records
// {Verb: "release", ClaimID: ...} per
// code:test/support/stores/stub/store/store.go::Store.Release.
func requireProducerRelease(t *testing.T, stub *stubstore.Store, claimID string) {
	t.Helper()
	calls := stub.Calls()
	for _, c := range calls {
		if c.Verb == "release" && c.ClaimID == claimID {
			return
		}
	}
	t.Fatalf("expected producer Release for claim_id=%s; recorded calls=%v", claimID, calls)
}

type durableOpts struct {
	instanceKey string
	selector    string
}

// startDurableHarness boots a single-node durable-acquirer template
// against a fresh stub producer. The acquirer's `stores:` declares
// Lifetime: durable so the auto-terminal Promote keeps the claim
// handle row past the dispatch terminal.
//
// Returns the harness, the acquirer node row, and the in-process stub
// *Store handle (used by Subcase E's release-observation check, which
// asserts the producer recorded a Release verb on the durable claim_id
// after the DELETE /v1/instances/{id} path fires
// ReleaseHeldDurableClaims).
func startDurableHarness(t *testing.T, opts durableOpts) (*scenario.Harness, *persistence.NodeRow, *stubstore.Store) {
	t.Helper()
	endpoint, stub, teardown := stubfixture.Start(t, stubstore.Config{
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
	h.Stub.WhenType("acquirer").Success(map[string]any{}, true, "owned")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "claim-handoff-durable-" + opts.instanceKey, Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "acquirer", Executor: "stub"},
				scenario.WithStores(node.NodeStoreRef{
					Name:     "queue-store",
					Selector: opts.selector,
					Intent:   "rw",
					Alias:    "asset",
					Lifetime: string(spec.ClaimLifetimeDurable),
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, opts.instanceKey, map[string]any{})

	acquirer := h.FindNode(iid, "acquirer")
	require.NotNil(t, acquirer)
	return h, acquirer, stub
}

type durableHandoffOpts struct {
	instanceKey   string
	selector      string
	alias         string
	coHolderType  string
	coHolderAttrs map[string]any
}

// startDurableHandoffHarness boots a durable-acquirer + co-holder
// template — the same shape as the in-pass-1 claim-handoff harness but
// with the acquirer's Lifetime set to durable so the row persists past
// the dispatch terminal.
func startDurableHandoffHarness(t *testing.T, opts durableHandoffOpts) (*scenario.Harness, *persistence.NodeRow, *persistence.NodeRow) {
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
	h.Stub.WhenType("acquirer").Success(map[string]any{}, true, "owned")
	h.Stub.WhenType(opts.coHolderType).Success(map[string]any{}, true, "co-held")

	wakeType := "test/wake/" + opts.coHolderType
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "claim-handoff-durable-" + opts.coHolderType, Version: "1",
		Messages: []spec.MessageSchema{
			{Type: wakeType},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "acquirer", Executor: "stub"},
				scenario.WithStores(node.NodeStoreRef{
					Name:     "queue-store",
					Selector: opts.selector,
					Intent:   "rw",
					Alias:    opts.alias,
					Lifetime: string(spec.ClaimLifetimeDurable),
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     opts.coHolderType,
					Executor: "stub",
					Holds: map[string]node.HoldsBinding{
						opts.alias: {From: "acquirer"},
					},
				},
				scenario.WithSubscribes(
					node.SubscriptionEntry{Node: "acquirer", Type: "terminal/success", WakeOnChange: spec.BoolPtr(true), ForceUpstreamRefresh: spec.BoolPtr(false)},
					node.SubscriptionEntry{Node: wakeType, Type: "terminal/success", WakeOnChange: spec.BoolPtr(true), ForceUpstreamRefresh: spec.BoolPtr(false)},
				),
				scenario.WithAttributes(opts.coHolderAttrs),
			),
		},
	})
	iid := h.CreateInstance(tid, opts.instanceKey, map[string]any{})

	acquirer := h.FindNode(iid, "acquirer")
	coHolder := h.FindNode(iid, opts.coHolderType)
	require.NotNil(t, acquirer)
	require.NotNil(t, coHolder)
	return h, acquirer, coHolder
}

// requireDurableCommittedHandle polls until the acquirer's claim_handle
// row reaches (state=committed, lifetime=durable). The Promote runs
// asynchronously after the holding subgraph completes, so a brief
// poll absorbs the supervisor-tick latency.
func requireDurableCommittedHandle(t *testing.T, h *scenario.Harness, acquirerNodeID shared.UUID) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var last *persistence.ClaimHandleRow
	for time.Now().Before(deadline) {
		row := readDurableClaimHandle(t, h, acquirerNodeID)
		if row != nil {
			last = row
			if row.State == spec.ClaimHandleStateCommitted && row.Lifetime == spec.ClaimLifetimeDurable {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if last == nil {
		require.Fail(t, "no claim_handle row found for acquirer")
		return
	}
	require.Failf(t, "claim_handle did not reach committed+durable",
		"last seen state=%s lifetime=%s", last.State, last.Lifetime)
}

// readDurableClaimHandle returns the durable claim_handle row whose
// holder_node_id equals acquirerNodeID, or nil if not yet inserted.
// Differs from readSingleClaimHandle (defined in claim_handoff_e2e_test.go)
// in that this helper tolerates a 0-rows result without failing — the
// row's existence is the property under test in the subcases that read
// it, so a missing row is a finding the caller asserts on.
func readDurableClaimHandle(t *testing.T, h *scenario.Harness, acquirerNodeID shared.UUID) *persistence.ClaimHandleRow {
	t.Helper()
	var out *persistence.ClaimHandleRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		rows, err := h.Persist.ClaimHandles().ListByHolderNode(h.Ctx, acquirerNodeID, tx)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		got, err := h.Persist.ClaimHandles().Get(h.Ctx, rows[0].ID, tx)
		if err != nil {
			return err
		}
		out = got
		return nil
	}))
	return out
}

// requireClaimHandleAbsent asserts the acquirer's claim_handle row is
// gone (the asset-Release path removes it entirely).
func requireClaimHandleAbsent(t *testing.T, h *scenario.Harness, acquirerNodeID shared.UUID) {
	t.Helper()
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		rows, err := h.Persist.ClaimHandles().ListByHolderNode(h.Ctx, acquirerNodeID, tx)
		if err != nil {
			return err
		}
		require.Empty(t, rows,
			"expected no claim_handle rows for the acquirer after asset Release; have %d", len(rows))
		return nil
	}))
}

// requireClaimHandleGoneByID asserts ClaimHandles().Get(id) returns
// nil — used by the instance-termination subcase where DELETE removes
// the row.
func requireClaimHandleGoneByID(t *testing.T, h *scenario.Harness, id shared.UUID) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var got *persistence.ClaimHandleRow
		require.NoError(t, h.InTx(func(tx persistence.Tx) error {
			r, err := h.Persist.ClaimHandles().Get(h.Ctx, id, tx)
			got = r
			return err
		}))
		if got == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.Fail(t, "claim_handle row was not deleted after DELETE /v1/instances/{id}")
}

// requireInstanceGone asserts the instance row was deleted by
// handleDeleteInstance.
func requireInstanceGone(t *testing.T, h *scenario.Harness, instanceID shared.UUID) {
	t.Helper()
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		row, err := h.Persist.Instances().Get(h.Ctx, instanceID, tx)
		if err != nil {
			return err
		}
		require.Nil(t, row, "expected the instance row to be deleted after DELETE")
		return nil
	}))
}

// requireSecondRun polls until a rimsky_node_runs row distinct from
// `prevRunID` appears for the given node. Used to confirm that an
// invalidate produced a new dispatch before asserting on the new
// run's substituted attributes.
func requireSecondRun(t *testing.T, h *scenario.Harness, nodeID, prevRunID shared.UUID, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var n int
		require.NoError(t, h.Pool.QueryRow(h.Ctx,
			`SELECT count(*) FROM rimsky_node_runs WHERE node_id = $1 AND id <> $2`,
			uuid.UUID(nodeID), uuid.UUID(prevRunID),
		).Scan(&n))
		if n > 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.Fail(t, "no second run row appeared for node after invalidate")
}

// requireSubstitutedAddrEquals reads the NodeAttributes row for the
// given run, fetches the named substituted key, and asserts the bytes
// (re-encoded as a JSON string to mirror the substitution engine's
// stringifyRaw unwrap) equal `wantAddress`. Mirrors the wire-payload-
// parity assertion shape used by Pass-1/Pass-2.
func requireSubstitutedAddrEquals(t *testing.T, h *scenario.Harness, runID shared.UUID, key string, wantAddress json.RawMessage, msg string) {
	t.Helper()
	var substituted any
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		attrs, err := h.Persist.NodeAttributes().GetByRun(h.Ctx, runID, tx)
		if err != nil {
			return err
		}
		require.NotNilf(t, attrs, "NodeAttributes row must exist for run %s", runID)
		substituted = attrs.Data[key]
		return nil
	}))
	gotStr, ok := substituted.(string)
	require.Truef(t, ok, "%s: substituted value should be a string; got %T", msg, substituted)
	gotEncoded, err := json.Marshal(gotStr)
	require.NoError(t, err)
	require.Equalf(t, []byte(wantAddress), gotEncoded, "%s", msg)
}

// requireSettlingSignalTypePrefix asserts the node row's
// settling_signal_type column starts with `prefix`. Used to pin
// terminal/error/acquire/unavailable on the competing acquirer.
func requireSettlingSignalTypePrefix(t *testing.T, h *scenario.Harness, nodeID shared.UUID, prefix string) {
	t.Helper()
	var row *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().Get(h.Ctx, nodeID, tx)
		row = r
		return err
	}))
	require.NotNil(t, row)
	require.NotNil(t, row.SettlingSignalType,
		"node %s must have a settling_signal_type after terminal", nodeID)
	require.True(t,
		len(*row.SettlingSignalType) >= len(prefix) &&
			(*row.SettlingSignalType)[:len(prefix)] == prefix,
		"node %s settling_signal_type=%q must start with %q",
		nodeID, *row.SettlingSignalType, prefix)
}

// deleteAsset hits DELETE /v1/instances/{id}/assets/{alias}. Asserts
// the response carries deleted=true.
func deleteAsset(t *testing.T, h *scenario.Harness, instanceID shared.UUID, assetAlias string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete,
		h.ControlBase+"/v1/instances/"+instanceID.String()+"/assets/"+assetAlias, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	require.Equalf(t, http.StatusOK, resp.StatusCode,
		"DELETE asset must return 200: %s", string(body))
	var out map[string]any
	require.NoError(t, json.Unmarshal(body, &out))
	require.Equal(t, true, out["deleted"], "DELETE response must report deleted=true")
}

// terminateInstance posts /v1/instances/{id}/terminate. Asserts 200.
func terminateInstance(t *testing.T, h *scenario.Harness, instanceID shared.UUID) {
	t.Helper()
	resp, err := http.Post(h.ControlBase+"/v1/instances/"+instanceID.String()+"/terminate",
		"application/json", bytes.NewReader([]byte(`{}`)))
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	require.Equalf(t, http.StatusOK, resp.StatusCode,
		"POST /terminate must return 200: %s", string(body))
}

// deleteInstance hits DELETE /v1/instances/{id}. Asserts 200 and
// deleted=true.
func deleteInstance(t *testing.T, h *scenario.Harness, instanceID shared.UUID) {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete,
		h.ControlBase+"/v1/instances/"+instanceID.String(), nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	require.Equalf(t, http.StatusOK, resp.StatusCode,
		"DELETE /v1/instances must return 200: %s", string(body))
	var out map[string]any
	require.NoError(t, json.Unmarshal(body, &out))
	require.Equal(t, true, out["deleted"],
		"DELETE response must report deleted=true")
}

// waitForInstanceTerminatedDurable polls until terminated_at is set on
// the instance row. Local to this file to avoid cross-file helper
// coupling; mirrors the waitForInstanceTerminatedInst helper in
// instance_lifecycle_fullstack_test.go.
func waitForInstanceTerminatedDurable(t *testing.T, h *scenario.Harness, iid shared.UUID, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var terminatedAt *time.Time
		h.QueryRowSQL(
			`SELECT terminated_at FROM rimsky_instances WHERE id = $1`,
			[]any{iid}, &terminatedAt)
		if terminatedAt != nil {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}
