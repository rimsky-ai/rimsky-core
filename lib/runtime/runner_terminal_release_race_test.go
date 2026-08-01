// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

func outboxRowCount(t *testing.T, tables persistence.Tables) int {
	t.Helper()
	ctx := context.Background()
	provider, ok := tables.(producerVerbOutboxProvider)
	require.True(t, ok, "backend must expose a producer-verb outbox for this test")
	var n int
	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, err := provider.ProducerVerbOutbox().ListAll(ctx, tx)
		n = len(rows)
		return err
	}))
	return n
}

func TestReleaseClaim_SkipsAlreadyResolvedRowInsteadOfDuplicateVerb(t *testing.T) {
	t.Parallel()
	args, acq, tables := seedRunningNodeForParkFixture(t)
	ctx := context.Background()

	producerName := "release-race-store"
	store := storetest.NewFake(producerName, claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	claimSpec := claimproducer.ClaimSpec{ProducerName: producerName, Selector: "/x", Intent: claimproducer.IntentReadWrite}
	claimID := shared.UUID(uuid.New())
	intent := "rw"
	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return tables.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 claimID,
			NodeRunID:          &acq.NodeRunID,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &producerName,
			ClaimScopeData:     []byte(`"scope"`),
			Address:            []byte(`"addr"`),
			Intent:             &intent,
			HolderSupervisorID: args.SupervisorID,
			HolderNodeID:       acq.NodeID,
			ExpiresAt:          time.Now().Add(10 * time.Minute),
			Lifetime:           spec.ClaimLifetimeSubgraph,
		}, tx)
	}))
	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return tables.ClaimHandles().Promote(ctx, claimID, args.SupervisorID, spec.ClaimHandleStateAbandoned, tx)
	}))

	lk := AcquiredLock{
		Spec:          claimSpec,
		ClaimHandleID: claimID,
		Producer:      store,
	}
	var post postCommitFn
	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		pc, err := releaseClaim(ctx, args, acq, lk, claimSpec, true, tx)
		post = pc
		return err
	}))
	if post != nil {
		post(ctx)
	}

	require.Equal(t, 0, outboxRowCount(t, tables),
		"releaseClaim must not enqueue a second producer verb for a claim a concurrent terminal path "+
			"already resolved out from under it (the row-lock + post-lock state re-check closes the race)")

	var row *persistence.ClaimHandleRow
	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := tables.ClaimHandles().Get(ctx, claimID, tx)
		row = r
		return err
	}))
	require.NotNil(t, row)
	require.Equal(t, spec.ClaimHandleStateAbandoned, row.State,
		"the concurrently-set outcome must stand; releaseClaim must not overwrite it")
}

func TestResolveLinkedSubClaimsInTx_SkipsAlreadyResolvedRowInsteadOfDuplicateVerb(t *testing.T) {
	t.Parallel()
	args, acq, tables := seedRunningNodeForParkFixture(t)
	ctx := context.Background()

	producerName := "resolve-linked-race-store"
	parentID := shared.UUID(uuid.New())
	subID := shared.UUID(uuid.New())
	intent := "rw"
	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := tables.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 parentID,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &producerName,
			ClaimScopeData:     []byte(`"parent-scope"`),
			Address:            []byte(`"parent-addr"`),
			Intent:             &intent,
			HolderSupervisorID: args.SupervisorID,
			HolderNodeID:       acq.NodeID,
			ExpiresAt:          time.Now().Add(10 * time.Minute),
		}, tx); err != nil {
			return err
		}
		return tables.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                  subID,
			NodeRunID:           &acq.NodeRunID,
			LockKind:            persistence.LockKindScope,
			ProducerName:        &producerName,
			ClaimScopeData:      []byte(`"sub-scope"`),
			Address:             []byte(`"sub-addr"`),
			Intent:              &intent,
			HolderSupervisorID:  args.SupervisorID,
			HolderNodeID:        acq.NodeID,
			ExpiresAt:           time.Now().Add(10 * time.Minute),
			Lifetime:            spec.ClaimLifetimeSubgraph,
			ParentClaimHandleID: &parentID,
		}, tx)
	}))
	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return tables.ClaimHandles().Promote(ctx, subID, args.SupervisorID, spec.ClaimHandleStateCommitted, tx)
	}))

	var post postCommitFn
	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		pc, err := resolveLinkedSubClaimsInTx(ctx, args, acq, true, tx)
		post = pc
		return err
	}))
	if post != nil {
		post(ctx)
	}

	require.Equal(t, 0, outboxRowCount(t, tables),
		"resolveLinkedSubClaimsInTx must not enqueue a second producer verb for a sub-claim a concurrent "+
			"terminal path already resolved out from under it")

	var row *persistence.ClaimHandleRow
	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := tables.ClaimHandles().Get(ctx, subID, tx)
		row = r
		return err
	}))
	require.NotNil(t, row)
	require.Equal(t, spec.ClaimHandleStateCommitted, row.State,
		"the concurrently-set outcome must stand; resolveLinkedSubClaimsInTx must not overwrite it")
}
