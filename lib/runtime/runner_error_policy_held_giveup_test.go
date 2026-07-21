// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

// @decision: held-as-state-not-phase
func TestApplyErrorPolicy_GiveUpWithActiveClaimDefersThroughHeldToAbandoned(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	args, acq, tables := seedHeldErrorFixture(t, cascade.NodeStateRunning, nil)

	producerName := "held-giveup-store"
	intent := "rw"
	claimID := shared.UUID(uuid.New())
	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return tables.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                     claimID,
			NodeRunID:              &acq.NodeRunID,
			LockKind:               persistence.LockKindScope,
			ProducerName:           &producerName,
			ClaimScopeData:         []byte(`"scope"`),
			Intent:                 &intent,
			RealizedWriteSemantics: "sync",
			HolderSupervisorID:     args.SupervisorID,
			HolderNodeID:           acq.NodeID,
			ExpiresAt:              time.Now().Add(time.Hour),
		}, tx)
	}))

	store := storetest.NewFake(producerName, claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	reg := locks.NewRegistry()
	reg.Add(producerName, store)
	args.StoreRegistry = reg
	acq.Locks = []AcquiredLock{{
		Alias:         "data",
		Spec:          claimproducer.ClaimSpec{ProducerName: producerName, Alias: "data", Intent: "rw"},
		ClaimHandleID: claimID,
		Producer:      store,
	}}

	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := applyErrorPolicyWithScratch(ctx, args, acq, "boom", "", nil, nil, nil, nil, tx)
		return err
	}))

	var runRow *persistence.NodeRunForGate
	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := tables.Nodes().GetRunForGate(ctx, acq.NodeRunID, tx)
		runRow = r
		return err
	}))
	require.NotNil(t, runRow)
	require.Equal(t, cascade.NodeStateFailed, runRow.State,
		"the run must eventually settle failed once its own claim resolves abandoned")

	var claimRow *persistence.ClaimHandleRow
	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := tables.ClaimHandles().Get(ctx, claimID, tx)
		claimRow = r
		return err
	}))
	require.NotNil(t, claimRow)
	require.Equal(t, spec.ClaimHandleStateAbandoned, claimRow.State,
		"a give_up while an active claim is still open must abandon that claim through the deferred "+
			"held/auto-terminal machinery, not bypass it with a raw unfiltered failed transition")

	var treeRow *persistence.NodeRunTreeRow
	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := tables.NodeRunTree().GetByID(ctx, acq.NodeRunID, tx)
		treeRow = r
		return err
	}))
	require.NotNil(t, treeRow, "expected a run-tree row to exist for the queue-seeded run")
	require.NotNil(t, treeRow.SettlingSignalType)
	require.Equal(t, "terminal/error/abandoned", *treeRow.SettlingSignalType,
		"the FINAL settling signal must be the uniform terminal/error/abandoned class fired by the "+
			"auto-terminal poison-rule resolution, not the run's own raw error class (terminal/error/boom) "+
			"escaping unfiltered straight from the give_up branch")
}
