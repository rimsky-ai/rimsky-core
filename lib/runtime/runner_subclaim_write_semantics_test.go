// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: claim-tree

package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

func fanOutOneSubClaim(
	ctx context.Context, t *testing.T, fx selfExclusionFixture, producer string, parentRunID shared.UUID,
	parentIntent claimproducer.Intent, parentRealized claimproducer.WriteSemantics, subScope []byte,
) shared.UUID {
	t.Helper()
	reg := locks.NewRegistry()
	store := storetest.NewFake(producer, claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{parentRealized},
		SupportsSplitScope:    true,
	})
	store.SplitClaimScopeFunc = func(req claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
		return claimproducer.SplitClaimScopeResponse{
			SubClaimScopes: []claimproducer.SubClaimScopeDescriptor{
				{PartitionKey: "alpha", ClaimScopeData: subScope},
			},
		}, nil
	}
	reg.Add(producer, store)
	args := RunArgs{
		Persist:               fx.tables,
		ClaimHandles:          fx.tables.ClaimHandles(),
		ClaimProducerRegistry: reg,
		Logger:                shared.SilentLogger{},
		Clock:                 shared.SystemClock{},
		SupervisorID:          "sup-A",
	}

	parentClaimID := shared.UUID(uuid.New())
	producerRef := producer
	intent := string(parentIntent)
	require.NoError(t, fx.tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return fx.tables.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                     parentClaimID,
			NodeRunID:              &parentRunID,
			LockKind:               persistence.LockKindScope,
			ProducerName:           &producerRef,
			ClaimScopeData:         []byte(`"parent-scope"`),
			Intent:                 &intent,
			RealizedWriteSemantics: string(parentRealized),
			HolderSupervisorID:     "sup-A",
			HolderNodeID:           fx.nodeID,
			ExpiresAt:              time.Now().Add(time.Hour),
		}, tx)
	}))

	var subClaims []SubClaim
	require.NoError(t, fx.tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		out, err := AcquireSubClaims(ctx, args, AcquireSubClaimsInput{
			ParentClaimHandleID:          parentClaimID,
			ProducerName:                 producer,
			NodeRunID:                    parentRunID,
			HolderNodeID:                 fx.nodeID,
			HolderSupervisorID:           "sup-A",
			InstanceID:                   fx.instanceID,
			LivenessInterval:             30 * time.Second,
			ParentIntent:                 string(parentIntent),
			ParentRealizedWriteSemantics: string(parentRealized),
		}, tx)
		subClaims = out
		return err
	}))
	require.Len(t, subClaims, 1)
	return subClaims[0].ClaimHandleID
}

func TestSubClaimInheritsParentRealizedWriteSemantics_OutsiderGetsNormalCoexistence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := openSelfExclusionFixture(ctx, t)

	const producer = "fanout-ws-store"
	parentRunID := seedSelfExclusionRun(ctx, t, fx)
	subClaimID := fanOutOneSubClaim(ctx, t, fx, producer, parentRunID,
		claimproducer.IntentRead, claimproducer.WriteSemanticsSync, []byte(`"shared-scope"`))

	require.NoError(t, fx.tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := fx.tables.ClaimHandles().Get(ctx, subClaimID, tx)
		if err != nil {
			return err
		}
		require.NotNil(t, row)
		require.Equal(t, string(claimproducer.WriteSemanticsSync), row.RealizedWriteSemantics,
			"sub-claim row must carry the parent's realized write semantics from insert")
		return nil
	}))

	outsiderRunID := seedSelfExclusionRun(ctx, t, fx)
	fake := storetest.NewFake(producer, claimproducer.Capabilities{})
	args := RunArgs{Persist: fx.tables, ClaimHandles: fx.tables.ClaimHandles(), SupervisorID: "sup-B"}
	cand := persistence.Candidate{NodeID: fx.nodeID, NodeRunID: outsiderRunID}

	readSpec := claimproducer.ClaimSpec{ProducerName: producer, Selector: "shared-scope", Intent: claimproducer.IntentRead}
	var conflicted bool
	require.NoError(t, fx.tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		conflicted, _, err = evaluateClaimScopeConflict(ctx, args, fake, readSpec, cand, tx)
		return err
	}))
	require.False(t, conflicted,
		"a read acquisition overlapping an active read/sync sub-claim must coexist — "+
			"exactly as if it overlapped the parent, not error on a missing realized value")

	rwSpec := claimproducer.ClaimSpec{ProducerName: producer, Selector: "shared-scope", Intent: claimproducer.IntentReadWrite}
	require.NoError(t, fx.tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		conflicted, _, err = evaluateClaimScopeConflict(ctx, args, fake, rwSpec, cand, tx)
		return err
	}))
	require.True(t, conflicted,
		"a read-write acquisition overlapping an active read/sync sub-claim must conflict "+
			"through the normal coexistence evaluation")
}
