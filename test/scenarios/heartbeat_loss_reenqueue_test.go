// Scenario 12 — heartbeat loss: a node appears running with a stale
// last_heartbeat_at. The scheduler's sweep transitions it running→stale,
// emits heartbeat_lost, and re-enqueues. In addition, an expired
// `rimsky_lock_holders` row owned by the same zombie supervisor is reaped
// by the §13.5 step-2 lock-holder sweep.
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

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
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
	_, err := h.Pool.Exec(h.Ctx,
		`UPDATE rimsky_nodes
		   SET state = 'running',
		       last_heartbeat_at = NOW() - INTERVAL '30 seconds',
		       assigned_supervisor_id = 'zombie-sup'
		 WHERE id = $1`,
		n.ID,
	)
	require.NoError(t, err)

	// Remove any auto-enqueued dispatch row so we can observe re-enqueue.
	_, err = h.Pool.Exec(h.Ctx, `DELETE FROM rimsky_dispatch WHERE node_id = $1`, n.ID)
	require.NoError(t, err)

	// Seed an expired `rimsky_lock_holders` row tied to the zombie node +
	// supervisor so the §13.5 step-2 sweep has something to reap. We pick
	// kind='named' to avoid pulling a real claim_store into the harness;
	// the sweep's per-row reap path is identical for all three kinds modulo
	// the store-side ReleaseLock call (claim-only).
	lockHolderID := uuid.New()
	lockName := "hb-loss-zombie-lock"
	require.NoError(t, h.Storage.Transaction(h.Ctx, func(ctx context.Context, tx storage.Tx) error {
		return h.Storage.LockHolders().Insert(ctx, storage.LockHolderInsertInput{
			ID:                 lockHolderID,
			LockKind:           storage.LockKindNamed,
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
		got, _ := h.Storage.Nodes().Get(h.Ctx, n.ID, nil)
		if got != nil && got.State == shared.NodeStateStale {
			sawStale = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.True(t, sawStale, "node did not transition running→stale")

	// Verify heartbeat_lost event.
	nid := n.ID
	evs, err := h.Storage.Events().List(h.Ctx, storage.EventListFilter{NodeID: &nid, Kind: "heartbeat_lost"},
		storage.ListPagination{Limit: 10}, nil)
	require.NoError(t, err)
	require.NotEmpty(t, evs.Events, "expected heartbeat_lost event")

	// A fresh dispatch row should exist (re-enqueued).
	var dispatchID uuid.UUID
	err = h.Pool.QueryRow(h.Ctx, `SELECT id FROM rimsky_dispatch WHERE node_id = $1`, n.ID).Scan(&dispatchID)
	require.NoError(t, err, "expected re-enqueued dispatch row")
	// And no claim is held against it (the dispatch row is a fresh
	// re-enqueue from the scheduler, not a survival of the zombie's claim).
	var claimedBy *string
	err = h.Pool.QueryRow(h.Ctx,
		`SELECT claimed_by FROM rimsky_dispatch WHERE id = $1`, dispatchID).Scan(&claimedBy)
	require.NoError(t, err)
	require.Nil(t, claimedBy, "re-enqueued dispatch row should not be claimed")

	// The §13.5 step-2 lock-holder sweep should reap the expired row we
	// seeded above. Poll rimsky_lock_holders directly until the row is gone.
	deadline = time.Now().Add(25 * time.Second)
	var reaped bool
	for time.Now().Before(deadline) {
		got, _ := h.Storage.LockHolders().Get(h.Ctx, lockHolderID, nil)
		if got == nil {
			reaped = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.True(t, reaped, "expired lock-holder row was not reaped by §13.5 step-2 sweep")

	// And a `lock_orphan_reaped` event was emitted for the reaped row.
	reapEvs, err := h.Storage.Events().List(h.Ctx,
		storage.EventListFilter{NodeID: &nid, Kind: "lock_orphan_reaped"},
		storage.ListPagination{Limit: 10}, nil)
	require.NoError(t, err)
	require.NotEmpty(t, reapEvs.Events, "expected lock_orphan_reaped event")
}
