// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Post-fold regression pin for the ownership-bail path
// (`runner_acquire_postcommit.go::handleOrphanedClaim`), the engine-
// route companion to `verify_before_run_post_commit_test.go`.
//
// The sibling test proves the verify-before-run bail FIRES (no
// dispatch, orphaned_claim_lost_race emitted). This test pins HOW the
// bail resolves the claims it is unwinding: through the unified
// claim-handle resolution engine (`ResolveClaimHandleTerminal`,
// OwnershipBail source) — the single audited verb-then-delete site.
// Pinned observables, against a real claim-producer over the wire:
//
//   - exactly one producer Abandon per acquired claim, targeting the
//     claim the Open minted (verb count + verb-then-delete ordering);
//   - the bail's own claim-handle row is deleted, while a decoy row
//     held by a DIFFERENT supervisor on the same node survives
//     (the deletion stays claimant-guarded, @blessed-invariant 4);
//   - no signal is emitted (admin path): zero terminal/* rows and
//     zero claim_resolution.* rows for the node — the only record is
//     the orphaned_claim_lost_race admin event.
//
// A regression that re-grows a hand-rolled delete at the bail site, or
// that routes the bail through the engine's Promote (leaving a
// state='abandoned' row) instead of the OwnershipBail delete, fails
// these assertions.
package scenarios

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/testfixture"
)

func TestVerifyBeforeRun_BailResolvesThroughEngine(t *testing.T) {
	t.Parallel()

	syncCaps := claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	}

	// @deliberate: Real store service over the wire: one seeded item so Open
	// succeeds and the acquisition tx commits — only then is the
	// post-commit steal window reachable. The Calls recorder counts the
	// engine-fired Abandon.
	endpointA, storeA, teardownA := stubfixture.Start(t, stubstore.Config{
		Capabilities: syncCaps,
		PickPolicies: map[string]stubstore.PickPolicyConfig{
			"@items": {
				OnCommit:     action.Action{Kind: action.Pop},
				OnGiveUp:     action.Action{Kind: action.Recycle},
				InitialItems: []json.RawMessage{json.RawMessage(`{"k":"v"}`)},
			},
		},
	})
	t.Cleanup(teardownA)

	// @deliberate: NoSupervisor: the test drives runtime.RunNode directly so it can
	// thread the PostCommitHook. The scheduler still runs and enqueues
	// the root dispatch row.
	h := scenario.Start(t, scenario.HarnessOpts{
		NoSupervisor: true,
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"store-a": {Endpoint: "grpc://" + endpointA, Capabilities: syncCaps},
			},
		},
	})
	h.Stub.WhenType("worker").Success(map[string]any{}, true, "should-not-run")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "bail-engine-route", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithStores(scenario.WriteClaimRef("store-a", "@items")),
			),
		},
	})
	// @constraint: Unique consumer key so -count=3 reruns never collide on the
	// instance-key uniqueness constraint.
	iid := h.CreateInstance(tid, "ck-bail-engine-"+uuid.NewString()[:8], map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)
	require.True(t, h.WaitForDispatch(worker.ID, 10*time.Second),
		"scheduler should enqueue the worker's dispatch row")

	var dispatchID uuid.UUID
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT id FROM rimsky_node_runs WHERE node_id = $1`, worker.ID,
	).Scan(&dispatchID))

	// @constraint: Decoy claim-handle row held by a DIFFERENT supervisor on the same
	// node. The bail must not touch it: the engine's OwnershipBail
	// delete operates only on the rows this supervisor's acquisition
	// created, and the delete itself is claimant-guarded
	// (@blessed-invariant 4). Far-future expiry keeps the periodic
	// reaper out of the picture.
	decoyID := uuid.New()
	decoyName := "bail-decoy-lock"
	require.NoError(t, h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.Persist.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 decoyID,
			LockKind:           persistence.LockKindNamed,
			LockName:           &decoyName,
			HolderSupervisorID: "other-supervisor",
			HolderNodeID:       worker.ID,
			ExpiresAt:          time.Now().Add(10 * time.Minute),
		}, tx)
	}))

	// @deliberate: Dial a real gRPC producer client — the same client type the
	// production supervisor registers.
	clientA, err := peer.Dial(h.Ctx, "store-a", "grpc://"+endpointA, peer.TLSModeOff)
	require.NoError(t, err)
	t.Cleanup(clientA.Close)
	registry := locks.NewRegistry()
	registry.Add("store-a", clientA)

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	// stolen records whether the post-commit hook actually fired —
	// guards against a silent regression where the seam stops being
	// invoked (which would make the test pass for the wrong reason).
	var stolen atomic.Bool
	args := runtime.RunArgs{
		Persist:           h.Persist,
		Queue:             h.Queue,
		ClaimHandles:      h.Persist.ClaimHandles(),
		AdvisoryLocker:    h.Driver.AdvisoryLocker(),
		StoreRegistry:     registry,
		Clock:             shared.SystemClock{},
		Logger:            shared.SilentLogger{},
		SupervisorID:      "scenario-runner",
		AcceptedExecutors: []string{"stub"},
		AcceptedStores:    []string{"store-a"},
		Pool:              pool,
		Resolver: executor.NewStaticResolver(map[string]executor.Endpoint{
			"stub": {Transport: "grpc", URL: h.StubAddr},
		}),
		LivenessInterval: 100 * time.Millisecond,
		// @deliberate: Force the cross-transaction ownership flip in the window
		// between the acquisition commit and the verify-before-run
		// separate-read. At hook time the acquisition has COMMITTED:
		// our claim-handle row exists, held by this supervisor, and the
		// producer has not yet seen an Abandon — so the row the engine
		// later deletes verifiably went through the verb-then-delete
		// sequence rather than never existing.
		PostCommitHook: func(ctx context.Context) {
			var held int
			require.NoError(t, h.Pool.QueryRow(ctx,
				`SELECT count(*) FROM rimsky_claim_handles
				  WHERE holder_node_id = $1 AND holder_supervisor_id = 'scenario-runner'`,
				worker.ID,
			).Scan(&held))
			require.Equal(t, 1, held,
				"at hook time the committed acquisition's claim-handle row must exist, held by this supervisor")
			require.Zero(t, countCalls(storeA.Calls(), "abandon"),
				"at hook time the producer must not have seen an Abandon yet (verb fires in the bail, after the steal)")
			tag, uerr := h.Pool.Exec(ctx,
				`UPDATE rimsky_node_runs SET claimed_by = 'thief-supervisor', claimed_at = NOW() WHERE id = $1`,
				dispatchID,
			)
			require.NoError(t, uerr)
			require.Equal(t, int64(1), tag.RowsAffected(),
				"post-commit hook should flip ownership of exactly the committed dispatch row")
			stolen.Store(true)
		},
	}

	out, err := runtime.RunNode(h.Ctx, args, nil)
	require.NoError(t, err)
	require.True(t, stolen.Load(),
		"post-commit hook must have fired — the acquisition tx committed and the verify window was reached")
	require.False(t, out.Ran,
		"verify-before-run must bail (Ran=false) when the claim was stolen between commit and the verify-read")

	// @constraint: Engine route, verb counts: exactly one Open, exactly one Abandon
	// (per the single acquired claim), targeting the same claim_id, and
	// never a Commit.
	callsA := storeA.Calls()
	require.Equal(t, 1, countCalls(callsA, "open"),
		"store-a must have been Open'd exactly once")
	require.Equal(t, 1, countCalls(callsA, "abandon"),
		"the bail must fire exactly one Abandon per acquired claim — no double-fire, no skip")
	var openClaimID, abandonClaimID string
	for _, c := range callsA {
		switch c.Verb {
		case "open":
			openClaimID = c.ClaimID
		case "abandon":
			abandonClaimID = c.ClaimID
		}
	}
	require.Equal(t, openClaimID, abandonClaimID,
		"the Abandon must target the claim the Open minted")
	require.Zero(t, countCalls(callsA, "commit"),
		"store-a must never see a Commit for a bailed acquisition")

	// @deliberate: Engine route, row disposition: the bail's own row is DELETED
	// (not promoted to state='abandoned' — the acquisition is unwound,
	// not resolved), while the foreign-held decoy survives untouched.
	var survivors []uuid.UUID
	rows, qerr := h.Pool.Query(h.Ctx,
		`SELECT id FROM rimsky_claim_handles WHERE holder_node_id = $1`, worker.ID)
	require.NoError(t, qerr)
	for rows.Next() {
		var id uuid.UUID
		require.NoError(t, rows.Scan(&id))
		survivors = append(survivors, id)
	}
	rows.Close()
	require.NoError(t, rows.Err())
	require.Equal(t, []uuid.UUID{decoyID}, survivors,
		"the bail's own claim-handle row must be deleted (no abandoned-state residue) and the other supervisor's decoy must survive — the deletion is claimant-guarded")

	// @deliberate: No signal (admin path): the node stays stale, no terminal/* and
	// no claim_resolution.* rows land on the event log.
	var got *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, gerr := h.Persist.Nodes().Get(h.Ctx, worker.ID, tx)
		got = r
		return gerr
	}))
	require.Equal(t, cascade.NodeStateStale, got.State,
		"node must remain stale — the bail fired before the running transition / dispatch")

	var signalCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_events
		  WHERE node_id = $1 AND (kind LIKE 'terminal/%' OR kind LIKE 'claim_resolution%')`,
		worker.ID,
	).Scan(&signalCount))
	require.Zero(t, signalCount,
		"the bail is an admin path — it must emit no terminal/* signal and no claim_resolution.* forensics")

	// @deliberate: The one admin record: exactly one orphaned_claim_lost_race for
	// the stolen dispatch.
	var orphanCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_events
		  WHERE kind = 'orphaned_claim_lost_race'
		    AND payload->>'dispatch_id' = $1`,
		dispatchID.String(),
	).Scan(&orphanCount))
	require.Equal(t, 1, orphanCount,
		"the bail must emit exactly one orphaned_claim_lost_race event for the stolen dispatch")

	// @constraint: Executor never invoked.
	require.Empty(t, h.Stub.Observed(),
		"the executor must not be invoked when the dispatch was stolen pre-verify")
}
