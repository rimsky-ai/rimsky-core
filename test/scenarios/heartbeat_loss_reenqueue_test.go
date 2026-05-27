// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario 12 — heartbeat loss: a node appears running with a stale
// last_heartbeat_at. The scheduler's sweep transitions it running→stale,
// emits heartbeat_lost, and re-enqueues. In addition, an expired
// `rimsky_claim_handles` row owned by the same zombie supervisor is reaped
// by the §7.5 step-2 lock-holder sweep.
//
// Migrated to the stores-redesign template grammar (spec §11): the worker
// node is built via scenario.MakeNode. The test then drives the
// scheduler's heartbeat-loss path manually by writing a stale heartbeat
// and seeding an expired lock-holder row, so no in-template store / lock
// wiring is required.
package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/graph/node"
	"github.com/rimsky-ai/rimsky-core/graph/scenario"
)

func TestHeartbeatLossReenqueue(t *testing.T) {
	t.Parallel()
	// Disable the supervisor so it doesn't race us claiming the row we
	// manufacture.
	h := scenario.Start(t, scenario.HarnessOpts{NoSupervisor: true})

	// Minimal template + instance + manually-inserted running node.
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "hb-loss", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-hb", map[string]any{})

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)

	// Force the node to running with a stale heartbeat (>>HeartbeatTimeout=5s).
	// Post-stage-3 cutover: state / last_heartbeat_at / claimed_by live
	// on rimsky_node_runs; rimsky_nodes carries only identity + frame_id.
	// The active zombie run row is seeded below.
	_, err := h.Pool.Exec(h.Ctx, `UPDATE rimsky_nodes SET updated_at = NOW() WHERE id = $1`, n.ID)
	require.NoError(t, err)

	// Replace any auto-enqueued dispatch row with an explicitly-seeded
	// active zombie row tied to the same supervisor + stale heartbeat
	// the rimsky_nodes mirror carries.
	_, err = h.Pool.Exec(h.Ctx, `DELETE FROM rimsky_node_runs WHERE node_id = $1`, n.ID)
	require.NoError(t, err)
	// Reuse the instance's already-running frame (the harness creates one
	// at instance-create time; uq_rimsky_frames_running enforces one
	// running frame per instance, so we read it rather than INSERT).
	var frameID uuid.UUID
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT frame_id FROM rimsky_frames WHERE instance_id = $1 AND state = 'running' LIMIT 1`,
		uuid.UUID(iid),
	).Scan(&frameID))
	mainScopeID := h.GetMainRunScopeID(iid)
	_, err = h.Pool.Exec(h.Ctx,
		`INSERT INTO rimsky_node_runs (id, node_id, executor_name, required_stores,
		                               enqueued_at, claimed_by, claimed_at,
		                               last_heartbeat_at, phase, state, frame_id, run_scope_id)
		 VALUES (gen_random_uuid(), $1, 'stub', '{}', NOW(), 'zombie-sup',
		         NOW() - INTERVAL '30 seconds', NOW() - INTERVAL '30 seconds',
		         'active', 'running', $2, $3)`,
		n.ID, frameID, mainScopeID,
	)
	require.NoError(t, err)

	// Seed an expired `rimsky_claim_handles` row tied to the zombie node +
	// supervisor so the §7.5 step-2 sweep has something to reap. We pick
	// kind='named' to avoid pulling a real claim_store into the harness;
	// the sweep's per-row reap path is identical for all three kinds modulo
	// the store-side ReleaseLock call (claim-only).
	lockHolderID := uuid.New()
	lockName := "hb-loss-zombie-lock"
	require.NoError(t, h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.Persist.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 lockHolderID,
			LockKind:           persistence.LockKindNamed,
			LockName:           &lockName,
			HolderSupervisorID: "zombie-sup",
			HolderNodeID:       n.ID,
			ExpiresAt:          time.Now().Add(-1 * time.Hour),
		}, tx)
	}))

	// Scheduler's stale-heartbeat sweep fires on each tick. Wait for the
	// node to flip to stale and a dispatch row to reappear.
	deadline := time.Now().Add(25 * time.Second)
	var sawStale bool
	for time.Now().Before(deadline) {
		var got *persistence.NodeRow
		_ = h.InTx(func(tx persistence.Tx) error {
			r, err := h.Persist.Nodes().Get(h.Ctx, n.ID, tx)
			got = r
			return err
		})
		if got != nil && got.State == cascade.NodeStateStale {
			sawStale = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.True(t, sawStale, "node did not transition running→stale")

	// Verify transient/heartbeat_missed signal event (canonical audit
	// row per concept:signal post-Pass-5).
	nid := n.ID
	var evs persistence.EventListResult
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Events().List(h.Ctx, persistence.EventListFilter{NodeID: &nid, Kind: "transient/heartbeat_missed"},
			persistence.ListPagination{Limit: 10}, tx)
		evs = r
		return err
	}))
	require.NotEmpty(t, evs.Events, "expected transient/heartbeat_missed signal event")

	// A fresh dispatch row should exist (re-enqueued).
	var dispatchID uuid.UUID
	require.NoError(t, h.Pool.QueryRow(h.Ctx, `SELECT id FROM rimsky_node_runs WHERE node_id = $1`, n.ID).Scan(&dispatchID),
		"expected re-enqueued dispatch row")
	// And no claim is held against it (the dispatch row is a fresh
	// re-enqueue from the scheduler, not a survival of the zombie's claim).
	var claimedBy *string
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT claimed_by FROM rimsky_node_runs WHERE id = $1`, dispatchID).Scan(&claimedBy))
	require.Nil(t, claimedBy, "re-enqueued dispatch row should not be claimed")

	// The §7.5 step-2 lock-holder sweep should reap the expired row we
	// seeded above. Poll rimsky_claim_handles directly until the row is gone.
	deadline = time.Now().Add(25 * time.Second)
	var reaped bool
	for time.Now().Before(deadline) {
		var got *persistence.ClaimHandleRow
		_ = h.InTx(func(tx persistence.Tx) error {
			r, err := h.Persist.ClaimHandles().Get(h.Ctx, lockHolderID, tx)
			got = r
			return err
		})
		if got == nil {
			reaped = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.True(t, reaped, "expired lock-holder row was not reaped by §7.5 step-2 sweep")

	// And a `lock_orphan_reaped` event was emitted for the reaped row.
	var reapEvs persistence.EventListResult
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Events().List(h.Ctx,
			persistence.EventListFilter{NodeID: &nid, Kind: "lock_orphan_reaped"},
			persistence.ListPagination{Limit: 10}, tx)
		reapEvs = r
		return err
	}))
	require.NotEmpty(t, reapEvs.Events, "expected lock_orphan_reaped event")
}
