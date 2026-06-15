// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Deterministic injection tests for the orphan-reaper vs
// in-flight-terminal overlap (`orphan_reaper.go::SweepOrphanedClaimHandles`
// racing the owning supervisor's terminal release).
//
// A claim-handle row at the edge of expiry is visible to BOTH paths at
// once: the reaper's ListExpired snapshot already holds it while the
// owning supervisor's terminal release is resolving it. Exactly one of
// the two may resolve the row; the claimant guard
// (@blessed-invariant 4) makes the loser a no-op. Per
// concept:terminal-resolution the reaper fires NO producer verb in
// either ordering — only the terminal release dispatches Commit/Abandon.
//
// Forcing the overlap deterministically requires an injection point in
// the reaper's list→delete window — `OrphanReaperArgs.PreReapHook`, a
// nil-default test-only seam. The defenses under test (the
// claimant-guarded DeleteIfExpired and the claimant-guarded Promote)
// run against real Postgres; nothing on those paths is stubbed.
//
//   - ReleaseWins: the owner's terminal release (the real
//     ResolveClaimHandleTerminal engine, firing the real producer verb
//     over the wire) completes inside the reaper's window. The reaper's
//     DeleteIfExpired must no-op and emit no lock_orphan_reaped event.
//   - ReaperWins: the sweep completes first; the owner's late terminal
//     release must not resurrect or double-resolve the row (Promote
//     no-ops claimant-guarded), and the verb it fires lands once
//     (at-least-once + claim_id idempotency on the store side).
package scenarios

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/testfixture"
)

// reaperRaceFixture is the shared setup for both interleavings: a
// scheduler-less / supervisor-less harness, one node, one terminal run
// row, one EXPIRED active claim-handle row owned by ownerSupervisor,
// and a real gRPC producer client for the terminal-release engine.
type reaperRaceFixture struct {
	h        *scenario.Harness
	store    *stubstore.Store
	producer locks.ClaimProducer
	chID     uuid.UUID
	owner    string
	args     runtime.RunArgs
}

func startReaperRaceFixture(t *testing.T) *reaperRaceFixture {
	t.Helper()
	syncCaps := claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	}
	// @deliberate: Scoped-direct stub store: Commit/Abandon record into the Calls
	// ledger; lookup is claim_id-based and idempotent.
	endpoint, store, teardown := stubfixture.Start(t, stubstore.Config{Capabilities: syncCaps})
	t.Cleanup(teardown)

	// @deliberate: No scheduler: the conductor tick would run the real orphan reaper
	// in the background and steal the seeded expired row from the
	// deterministic interleaving. No supervisor: the test drives the
	// terminal release itself.
	h := scenario.Start(t, scenario.HarnessOpts{NoScheduler: true, NoSupervisor: true})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "orphan-reaper-terminal-race", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-reaper-terminal-race", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)
	require.NotNil(t, worker.FrameID)
	mainScopeID := h.GetMainRunScopeID(iid)

	const owner = "owner-supervisor"
	h.ExecSQL(`DELETE FROM rimsky_node_runs WHERE node_id = $1`, worker.ID)
	runID := uuid.New()
	h.ExecSQL(
		`INSERT INTO rimsky_node_runs (id, node_id, executor_name, required_stores, enqueued_at, frame_id, run_scope_id, phase)
		 VALUES ($1, $2, 'stub', '{}', NOW() - INTERVAL '10 minutes', $3, $4, 'completed')`,
		runID, worker.ID, *worker.FrameID, mainScopeID,
	)
	// @deliberate: The row is past expiry but still active and owned — the edge the
	// reaper and the in-flight terminal both observe.
	chID := uuid.New()
	h.ExecSQL(
		`INSERT INTO rimsky_claim_handles
		   (id, node_run_id, lock_kind, producer_name, claim_scope_data, address, intent,
		    is_held, holder_supervisor_id, holder_node_id, expires_at, frame_id, state)
		 VALUES ($1, $2, 'claim_scope', 'reap-store', '"@thing"', '"@thing"', 'rw',
		         FALSE, $3, $4, NOW() - INTERVAL '1 minute', $5, 'active')`,
		chID, runID, owner, worker.ID, *worker.FrameID,
	)

	client, err := peer.Dial(h.Ctx, "reap-store", "grpc://"+endpoint, peer.TLSModeOff)
	require.NoError(t, err)
	t.Cleanup(client.Close)
	registry := locks.NewRegistry()
	registry.Add("reap-store", client)

	return &reaperRaceFixture{
		h:        h,
		store:    store,
		producer: client,
		chID:     chID,
		owner:    owner,
		args: runtime.RunArgs{
			Persist:       h.Persist,
			Queue:         h.Queue,
			ClaimHandles:  h.Persist.ClaimHandles(),
			StoreRegistry: registry,
			Clock:         shared.SystemClock{},
			Logger:        shared.SilentLogger{},
			SupervisorID:  owner,
		},
	}
}

// releaseTerminal runs the owning supervisor's terminal release for the
// fixture's claim handle through the real unified terminal-decision
// engine (the same ResolveClaimHandleTerminal call
// runner_terminal_release.go::releaseClaim makes for a non-held claim).
func (f *reaperRaceFixture) releaseTerminal(t *testing.T, ctx context.Context) error {
	t.Helper()
	return f.h.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return runtime.ResolveClaimHandleTerminal(ctx, f.args, tx, runtime.TerminalDecision{
			ClaimHandleID: f.chID,
			SupervisorID:  f.owner,
			Source:        runtime.ActiveTerminal,
			Outcome:       runtime.AggregateCommit,
			Producer:      f.producer,
			Scope:         []byte(`"@thing"`),
			Address:       []byte(`"@thing"`),
			ProducerName:  "reap-store",
		})
	})
}

// countStoreVerb counts the recorded producer calls matching verb.
func (f *reaperRaceFixture) countStoreVerb(verb string) int {
	n := 0
	for _, c := range f.store.Calls() {
		if c.Verb == verb {
			n++
		}
	}
	return n
}

// reapEvents counts lock_orphan_reaped events for the fixture's claim
// handle.
func (f *reaperRaceFixture) reapEvents(t *testing.T) int {
	t.Helper()
	var n int
	require.NoError(t, f.h.Pool.QueryRow(f.h.Ctx,
		`SELECT count(*) FROM rimsky_events
		  WHERE kind = 'lock_orphan_reaped' AND payload->>'claim_handle_id' = $1`,
		f.chID.String(),
	).Scan(&n))
	return n
}

func TestOrphanReaperVsTerminalRelease_ReleaseWinsInsideSweepWindow(t *testing.T) {
	t.Parallel()
	f := startReaperRaceFixture(t)

	// @deliberate: The reaper has listed the expired row; the owner's terminal
	// release completes inside the list→delete window. The reaper's
	// claimant-guarded DeleteIfExpired must then lose: Promote nulled
	// the holder, so the guard matches nothing.
	var hooked atomic.Bool
	require.NoError(t, runtime.SweepOrphanedClaimHandles(f.h.Ctx, runtime.OrphanReaperArgs{
		Persist:      f.h.Persist,
		ClaimHandles: f.h.Persist.ClaimHandles(),
		Logger:       shared.SilentLogger{},
		PreReapHook: func(ctx context.Context, claimHandleID shared.UUID) {
			require.Equal(t, f.chID, uuid.UUID(claimHandleID),
				"the reaper's window must hold exactly the seeded expired row")
			require.Zero(t, f.countStoreVerb("commit"),
				"at hook time the terminal release has not fired yet")
			require.NoError(t, f.releaseTerminal(t, ctx),
				"the owning supervisor's terminal release must succeed inside the reaper's window")
			hooked.Store(true)
		},
	}))
	require.True(t, hooked.Load(), "the PreReapHook seam must have fired")

	// @deliberate: Exactly one of the two paths resolved the row: the release
	// promoted it; the reaper's delete was a no-op.
	var rowCount int
	var state string
	f.h.QueryRowSQL(`SELECT count(*) FROM rimsky_claim_handles WHERE id = $1`, []any{f.chID}, &rowCount)
	require.Equal(t, 1, rowCount, "the reaper must NOT have deleted the row the release already resolved")
	f.h.QueryRowSQL(`SELECT state FROM rimsky_claim_handles WHERE id = $1`, []any{f.chID}, &state)
	require.Equal(t, "committed", state, "the release's Promote is the single resolution")

	// @constraint: Verb accounting per concept:terminal-resolution: the release fired
	// Commit exactly once; the reaper fired nothing.
	require.Equal(t, 1, f.countStoreVerb("commit"),
		"exactly one producer Commit — the terminal release's")
	require.Zero(t, f.countStoreVerb("abandon"), "no Abandon may fire")

	// @constraint: The losing reaper must not claim the reap in the audit log.
	require.Zero(t, f.reapEvents(t),
		"no lock_orphan_reaped event may be emitted when the reaper lost the race")
}

func TestOrphanReaperVsTerminalRelease_ReaperWinsThenLateRelease(t *testing.T) {
	t.Parallel()
	f := startReaperRaceFixture(t)

	// @deliberate: The sweep completes first: the reaper hard-deletes the expired
	// row claimant-guarded, emits lock_orphan_reaped, and fires NO
	// producer verb.
	require.NoError(t, runtime.SweepOrphanedClaimHandles(f.h.Ctx, runtime.OrphanReaperArgs{
		Persist:      f.h.Persist,
		ClaimHandles: f.h.Persist.ClaimHandles(),
		Logger:       shared.SilentLogger{},
	}))
	var rowCount int
	f.h.QueryRowSQL(`SELECT count(*) FROM rimsky_claim_handles WHERE id = $1`, []any{f.chID}, &rowCount)
	require.Zero(t, rowCount, "the reaper must have deleted the expired row")
	require.Equal(t, 1, f.reapEvents(t), "the winning reaper emits exactly one lock_orphan_reaped event")
	require.Zero(t, f.countStoreVerb("commit"), "the reaper fires no producer verb")
	require.Zero(t, f.countStoreVerb("abandon"), "the reaper fires no producer verb")

	// @constraint: The owner's late terminal release arrives after the reap. It must
	// not error, must not resurrect the row, and must not double-record
	// the resolution: the claimant-guarded Promote matches nothing
	// (logged no-op). Its producer verb still fires once — at-least-once
	// delivery; the store's claim_id idempotency absorbs it.
	require.NoError(t, f.releaseTerminal(t, f.h.Ctx),
		"the losing terminal release must no-op cleanly, not error")
	f.h.QueryRowSQL(`SELECT count(*) FROM rimsky_claim_handles WHERE id = $1`, []any{f.chID}, &rowCount)
	require.Zero(t, rowCount, "the late release must not resurrect the reaped row")
	require.Equal(t, 1, f.reapEvents(t), "still exactly one lock_orphan_reaped event")
	require.Equal(t, 1, f.countStoreVerb("commit"),
		"exactly one producer Commit total — the release's at-least-once verb, none from the reaper")
}
