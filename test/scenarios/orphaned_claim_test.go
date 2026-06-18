// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

	payload := evs.Events[0].Payload
	require.Equal(t, "named", payload["lock_kind"])
	require.Equal(t, "dead-supervisor", payload["supervisor_id"])
}
