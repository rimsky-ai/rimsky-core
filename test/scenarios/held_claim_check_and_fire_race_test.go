// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestHeldClaimCheckAndFire_FiresExactlyOnceUnderRacingFinals(t *testing.T) {
	t.Parallel()

	syncCaps := claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	}
	endpoint, store, teardown := stubfixture.Start(t, stubstore.Config{Capabilities: syncCaps})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		NoScheduler:  true,
		NoSupervisor: true,
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				"held-store": {Endpoint: "grpc://" + endpoint, Capabilities: syncCaps},
			},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "held-check-and-fire-race", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "acquirer", Executor: "stub"},
				scenario.WithClaimProducers(scenario.AliasedClaimRef("held-store", "@thing", "rw", "held")),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "inheritor",
					Executor: "stub",
					Holds:    map[string]node.HoldsBinding{"held": {From: "acquirer"}},
				},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "acquirer", Type: "terminal/success", ForceUpstreamRefresh: node.BoolPtr(false)}),
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

	h.ExecSQL(`DELETE FROM rimsky_node_runs WHERE node_id IN ($1, $2)`, acq.ID, inh.ID)
	acqRunID := uuid.New()
	inhRunID := uuid.New()
	h.ExecSQL(
		`INSERT INTO rimsky_node_runs (id, node_id, executor_name, required_stores, enqueued_at, frame_id, run_scope_id, state, creation_reason, sequence)
		 VALUES ($1, $2, 'stub', '{}', NOW() - INTERVAL '10 seconds', $3, $4, 'fresh', 'cascade', 1),
		        ($5, $6, 'stub', '{}', NOW() - INTERVAL '10 seconds', $3, $4, 'fresh', 'cascade', 1)`,
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

	var (
		bErr      error
		bDone     = make(chan struct{})
		startB    sync.Once
		hookFired bool
	)
	runB := func() {
		defer close(bDone)
		var post func(context.Context)
		bErr = h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
			pc, err := runtime.CheckAndFireResolution(ctx, baseArgs, tx, chID)
			post = pc
			return err
		})
		if bErr == nil && post != nil {
			post(h.Ctx)
		}
	}

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
			t.Cleanup(func() { <-bDone })
		})
		require.True(t, waitForBlockedContender(ctx),
			"contender B must be observably blocked on the claim-handle row lock inside A's check→fire window")
	}

	var postA func(context.Context)
	require.NoError(t, h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		pc, err := runtime.CheckAndFireResolution(ctx, argsA, tx, chID)
		postA = pc
		return err
	}))
	if postA != nil {
		postA(h.Ctx)
	}
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

	var rowCount int
	var state string
	var holder *string
	h.QueryRowSQL(`SELECT count(*) FROM rimsky_claim_handles WHERE id = $1`, []any{chID}, &rowCount)
	require.Equal(t, 1, rowCount, "exactly one claim-handle row must remain")
	h.QueryRowSQL(`SELECT state, holder_supervisor_id FROM rimsky_claim_handles WHERE id = $1`,
		[]any{chID}, &state, &holder)
	require.Equal(t, "committed", state, "the row must be promoted to committed exactly once")
	require.Nil(t, holder, "Promote must null the holder (absence guard for later sweeps)")

	var resolutionEvents int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_events WHERE kind = 'claim_resolution.commit' AND payload->>'claim_handle_id' = $1`,
		chID.String(),
	).Scan(&resolutionEvents))
	require.Equal(t, 1, resolutionEvents,
		"exactly one claim_resolution.commit event must be emitted for the claim handle")
}
