// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario 13 — orphaned claim: a `rimsky_claim_handles` row outlives its
// supervisor's heartbeat. The scheduler's §7.5 step-2 lock-holder sweep
// reaps the expired row, deletes it claimant-guarded on
// `holder_supervisor_id`, and emits a `lock_orphan_reaped` event.
//
// Migrated to the stores-redesign template grammar (spec §11). The legacy
// dispatch-row claim-orphan path (`rimsky_node_runs.claimed_by` >
// 5 × heartbeat_timeout) is still wired in `graph/scheduler/scheduler.go` and
// emits `orphaned_claim_released`, but the redesign relocates the
// supervisor-side orphan signal onto `rimsky_claim_handles` (§9.9.2 +
// §7.5). This scenario exercises the lock-holder path; `verify_before_run_
// race_test.go` covers the dispatch-row complement.
package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestOrphanedClaim(t *testing.T) {
	t.Parallel()
	// NoSupervisor so the harness's running supervisor doesn't claim the
	// node we're about to attach a manufactured lock-holder row to.
	h := scenario.Start(t, scenario.HarnessOpts{NoSupervisor: true})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "orphan", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-orphan", map[string]any{})
	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)

	// Seed an expired `rimsky_claim_handles` row tied to a dead supervisor.
	// We pick `kind='named'` so the per-row reap path runs without needing a
	// real claim_store factory in the harness — the §7.5 step-2 reap is
	// identical for all three kinds modulo the store-side ReleaseLock call
	// (claim-only). Same pattern as `heartbeat_loss_reenqueue_test.go`.
	lockHolderID := uuid.New()
	lockName := "orphan-zombie-lock"
	require.NoError(t, h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.Persist.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 lockHolderID,
			LockKind:           persistence.LockKindNamed,
			LockName:           &lockName,
			HolderSupervisorID: "dead-supervisor",
			HolderNodeID:       n.ID,
			ExpiresAt:          time.Now().Add(-2 * time.Minute),
		}, tx)
	}))

	// Wait for the §7.5 step-2 sweep to reap the row. Scheduler tick
	// interval in the harness is 250ms; orphan-reap cutoff is purely
	// `expires_at < now()` for the lock-holder path.
	deadline := time.Now().Add(20 * time.Second)
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

	// `lock_orphan_reaped` event was emitted with the reaped row's metadata.
	nid := n.ID
	var evs persistence.EventListResult
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Events().List(h.Ctx,
			persistence.EventListFilter{NodeID: &nid, Kind: "lock_orphan_reaped"},
			persistence.ListPagination{Limit: 10}, tx)
		evs = r
		return err
	}))
	require.NotEmpty(t, evs.Events, "expected lock_orphan_reaped event")

	// Spot-check the payload carries the kind + supervisor for operator
	// triage (sweep_locks.go:lockReapPayload populates these unconditionally).
	payload := evs.Events[0].Payload
	require.Equal(t, "named", payload["lock_kind"])
	require.Equal(t, "dead-supervisor", payload["supervisor_id"])
}
