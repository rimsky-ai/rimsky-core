// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
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

	claimHandleID := uuid.New()
	lockName := "orphan-zombie-lock"
	require.NoError(t, h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.Persist.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 claimHandleID,
			LockKind:           persistence.LockKindNamed,
			LockName:           &lockName,
			HolderSupervisorID: "dead-supervisor",
			HolderNodeID:       n.ID,
			ExpiresAt:          time.Now().Add(-2 * time.Minute),
		}, tx)
	}))

	waitForClaimHandleReaped(t, h, claimHandleID)

	nid := n.ID
	var evs persistence.EventListResult
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Events().List(h.Ctx,
			persistence.EventListFilter{NodeID: &nid, KindIn: []string{"lock_orphan_reaped"}},
			persistence.ListPagination{Limit: 10}, tx)
		evs = r
		return err
	}))
	require.NotEmpty(t, evs.Events, "expected lock_orphan_reaped event")

	payload := evs.Events[0].Payload.Map()
	require.Equal(t, "named", payload["lock_kind"])
	require.Equal(t, "dead-supervisor", payload["supervisor_id"])
}

func TestOrphanedClaimActiveRowsAlwaysHaveHolder(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{NoSupervisor: true})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "orphan-active-holder-invariant", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-orphan-active-holder-invariant", map[string]any{})
	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)

	claimHandleID := uuid.New()
	lockName := "orphan-active-holder-invariant-lock"
	require.NoError(t, h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		return h.Persist.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 claimHandleID,
			LockKind:           persistence.LockKindNamed,
			LockName:           &lockName,
			HolderSupervisorID: "some-supervisor",
			HolderNodeID:       n.ID,
			ExpiresAt:          time.Now().Add(-2 * time.Minute),
		}, tx)
	}))

	_, err := h.Pool.Exec(h.Ctx,
		`UPDATE rimsky_claim_handles SET holder_supervisor_id = NULL WHERE id = $1`, claimHandleID)
	require.Error(t, err,
		"rimsky_claim_handles_active_has_holder must reject a NULL holder_supervisor_id on an "+
			"active row; the orphan-reaper's nil-holder skip (reapOneClaimHandle) is unreachable "+
			"dead code as long as this constraint holds — an active row can never lack a holder")
}

func waitForClaimHandleReaped(t *testing.T, h *scenario.Harness, id uuid.UUID) {
	t.Helper()
	awaited.Until(t, "claim_handle row "+id.String()+" to be reaped", func() bool {
		var got *persistence.ClaimHandleRow
		if err := h.InTx(func(tx persistence.Tx) error {
			r, err := h.Persist.ClaimHandles().Get(h.Ctx, id, tx)
			got = r
			return err
		}); err != nil {
			t.Fatalf("waitForClaimHandleReaped: probe failed: %v", err)
		}
		return got == nil
	})
}
