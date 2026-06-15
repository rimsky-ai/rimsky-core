// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Deterministic injection test for the held-claim aggregate
// check-and-fire (`auto_terminal.go::CheckAndFireResolution`,
// @blessed-invariant 13: at holding-subgraph completion exactly one
// producer verb fires).
//
// Two co-holding node-runs (acquirer + `holds:` inheritor) have both
// reached terminal — the racy shape is two supervisor goroutines
// observing the completed subgraph nearly simultaneously and each
// running the check-and-fire. The defense is the SELECT … FOR UPDATE
// on the rimsky_claim_handles row plus the state='active' guard: the
// second contender blocks on the row lock until the first commits,
// then observes state='committed' and no-ops.
//
// Forcing the interleaving deterministically requires an injection
// point between the first contender's check (subgraph complete, fire
// decided) and its fire (producer verb + Promote) —
// `RunArgs.CheckAndFireHook`, a nil-default test-only seam. The hook
// launches the second contender and waits until it is observably
// blocked on the row lock (pg_stat_activity), so the second check
// provably runs after the first check but before the first fire. The
// defense itself (real Postgres row lock + state guard) is NOT stubbed.
package scenarios

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
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

func TestHeldClaimCheckAndFire_FiresExactlyOnceUnderRacingFinals(t *testing.T) {
	t.Parallel()

	syncCaps := claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	}
	// @deliberate: Scoped-direct stub store (no pick policies): Open echoes the
	// selector; Commit/Abandon record into the Calls ledger the test
	// counts.
	endpoint, store, teardown := stubfixture.Start(t, stubstore.Config{Capabilities: syncCaps})
	t.Cleanup(teardown)

	// @constraint: No scheduler / no supervisor: the test drives the check-and-fire
	// directly so it can thread the injection hook; background sweeps
	// must not touch the seeded rows.
	h := scenario.Start(t, scenario.HarnessOpts{
		NoScheduler:  true,
		NoSupervisor: true,
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"held-store": {Endpoint: "grpc://" + endpoint, Capabilities: syncCaps},
			},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "held-check-and-fire-race", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "acquirer", Executor: "stub"},
				scenario.WithStores(scenario.AliasedClaimRef("held-store", "@thing", "rw", "held")),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "inheritor",
					Executor: "stub",
					Holds:    map[string]node.HoldsBinding{"held": {From: "acquirer"}},
				},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "acquirer", Type: "terminal/success"}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-held-check-fire-race", map[string]any{})

	acq := h.FindNode(iid, "acquirer")
	inh := h.FindNode(iid, "inheritor")
	require.NotNil(t, acq)
	require.NotNil(t, inh)
	require.NotNil(t, acq.FrameID, "acquirer should carry a frame_id from the initial frame advance")
	frameID := *acq.FrameID
	mainScopeID := h.GetMainRunScopeID(iid)
	const supervisorID = "race-supervisor"

	// @deliberate: Seed the post-terminal shape directly: both members' run rows are
	// already terminal, both claim-holders rows are 'completed', the
	// claim-handle row is still active and owned. This is exactly the
	// state the LAST terminating member's release tx observes when it
	// invokes CheckAndFireResolution.
	h.ExecSQL(`DELETE FROM rimsky_node_runs WHERE node_id IN ($1, $2)`, acq.ID, inh.ID)
	acqRunID := uuid.New()
	inhRunID := uuid.New()
	h.ExecSQL(
		`INSERT INTO rimsky_node_runs (id, node_id, executor_name, required_stores, enqueued_at, frame_id, run_scope_id, phase)
		 VALUES ($1, $2, 'stub', '{}', NOW() - INTERVAL '10 seconds', $3, $4, 'completed'),
		        ($5, $6, 'stub', '{}', NOW() - INTERVAL '10 seconds', $3, $4, 'completed')`,
		acqRunID, acq.ID, frameID, mainScopeID, inhRunID, inh.ID,
	)
	chID := uuid.New()
	h.ExecSQL(
		`INSERT INTO rimsky_claim_handles
		   (id, node_run_id, lock_kind, producer_name, claim_scope_data, address, intent,
		    is_held, holder_supervisor_id, holder_node_id, expires_at, frame_id, state)
		 VALUES ($1, $2, 'claim_scope', 'held-store', '"@thing"', '"@thing"', 'rw',
		         TRUE, $3, $4, NOW() + INTERVAL '10 minutes', $5, 'active')`,
		chID, acqRunID, supervisorID, acq.ID, frameID,
	)
	h.ExecSQL(
		`INSERT INTO rimsky_claim_holders (id, claim_handle_id, holder_run_id, state, completed_at, frame_id)
		 VALUES ($1, $2, $3, 'completed', NOW(), $4),
		        ($5, $2, $6, 'completed', NOW(), $4)`,
		uuid.New(), chID, acqRunID, frameID, uuid.New(), inhRunID,
	)

	client, err := peer.Dial(h.Ctx, "held-store", "grpc://"+endpoint, peer.TLSModeOff)
	require.NoError(t, err)
	t.Cleanup(client.Close)
	registry := locks.NewRegistry()
	registry.Add("held-store", client)

	baseArgs := runtime.RunArgs{
		Persist:       h.Persist,
		Queue:         h.Queue,
		ClaimHandles:  h.Persist.ClaimHandles(),
		StoreRegistry: registry,
		Clock:         shared.SystemClock{},
		Logger:        shared.SilentLogger{},
		SupervisorID:  supervisorID,
	}

	// @deliberate: Contender B: same check-and-fire, no hook. Launched from inside
	// contender A's check→fire window.
	var (
		bErr      error
		bDone     = make(chan struct{})
		startB    sync.Once
		hookFired bool
	)
	runB := func() {
		defer close(bDone)
		bErr = h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
			return runtime.CheckAndFireResolution(ctx, baseArgs, tx, chID)
		})
	}

	// @deliberate: waitForBlockedContender polls pg_stat_activity until a backend in
	// this database is lock-waiting on the claim-handle FOR UPDATE read.
	// That observation is what makes the interleaving deterministic: B's
	// check has STARTED (it reached LockForUpdate) while A holds the row
	// lock between its check and its fire.
	waitForBlockedContender := func(ctx context.Context) bool {
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			var waiting int
			if err := h.Pool.QueryRow(ctx,
				`SELECT count(*) FROM pg_stat_activity
				  WHERE datname = current_database()
				    AND wait_event_type = 'Lock'
				    AND query LIKE '%FROM rimsky_claim_handles WHERE id = $1 FOR UPDATE%'`,
			).Scan(&waiting); err == nil && waiting > 0 {
				return true
			}
			time.Sleep(10 * time.Millisecond)
		}
		return false
	}

	argsA := baseArgs
	argsA.CheckAndFireHook = func(ctx context.Context) {
		hookFired = true
		startB.Do(func() {
			go runB()
			// @deliberate: Join B's goroutine even when an assertion below FailNows
			// before the inline <-bDone select — otherwise B outlives the
			// test body and races teardown. The hook runs on the test
			// goroutine (A's Transaction executes it synchronously), so
			// t.Cleanup is legal here. bDone is CLOSED (not a one-shot
			// send), so the Cleanup receive and the inline receive both
			// complete.
			t.Cleanup(func() { <-bDone })
		})
		require.True(t, waitForBlockedContender(ctx),
			"contender B must be observably blocked on the claim-handle row lock inside A's check→fire window")
	}

	// @deliberate: Contender A: the first check-and-fire, paused between check and
	// fire by the hook.
	require.NoError(t, h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		return runtime.CheckAndFireResolution(ctx, argsA, tx, chID)
	}))
	require.True(t, hookFired, "the CheckAndFireHook seam must have fired")

	select {
	case <-bDone:
	case <-time.After(30 * time.Second):
		t.Fatal("contender B did not finish after A committed")
	}
	require.NoError(t, bErr, "the losing contender must no-op cleanly, not error")

	commits := 0
	for _, c := range store.Calls() {
		switch c.Verb {
		case "commit":
			commits++
		case "abandon":
			t.Fatalf("no Abandon may fire for an all-completed holding subgraph (claim %s)", c.ClaimID)
		}
	}
	require.Equal(t, 1, commits,
		"the aggregate check-and-fire must fire the producer verb exactly once under racing finals")

	// @deliberate: The claim-handle row resolved exactly once: promoted to
	// 'committed' (the Promote nulls the holder per the CHECK pair), not
	// deleted, not double-transitioned.
	var rowCount int
	var state string
	var holder *string
	h.QueryRowSQL(`SELECT count(*) FROM rimsky_claim_handles WHERE id = $1`, []any{chID}, &rowCount)
	require.Equal(t, 1, rowCount, "exactly one claim-handle row must remain")
	h.QueryRowSQL(`SELECT state, holder_supervisor_id FROM rimsky_claim_handles WHERE id = $1`,
		[]any{chID}, &state, &holder)
	require.Equal(t, "committed", state, "the row must be promoted to committed exactly once")
	require.Nil(t, holder, "Promote must null the holder (absence guard for later sweeps)")

	// @deliberate: Exactly one claim_resolution.commit forensics event — a second
	// fire would have emitted a duplicate.
	var resolutionEvents int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_events WHERE kind = 'claim_resolution.commit' AND payload->>'claim_handle_id' = $1`,
		chID.String(),
	).Scan(&resolutionEvents))
	require.Equal(t, 1, resolutionEvents,
		"exactly one claim_resolution.commit event must be emitted for the claim handle")
}
