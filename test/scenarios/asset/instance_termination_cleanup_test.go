// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package asset

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks/storetest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	pgtest "github.com/rimsky-ai/rimsky-core/test/support/pgmigrate"
)

func TestInstanceTerminationCleanup_PreservesFailedReleaseForRetry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplateAsset(ctx, t, backend, node.TemplateSpec{
		Name: "instance-termination-cleanup", Version: "1",
	})
	instID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	ck := "ck-instance-termination-cleanup"
	var acqNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID: mainScopeID, GraphName: "main", InstanceID: instID,
		}); err != nil {
			return err
		}
		if _, err := backend.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: instID, TemplateHash: tmpl.ID,
			InstanceKey: &ck, Params: map[string]any{},
		}, tx); err != nil {
			return err
		}
		a, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: instID,
			NodeType: "acquirer", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		acqNode = a
		return nil
	}))

	reg := locks.NewRegistry()
	storeName := "workspace"
	stubStore := storetest.NewFake(storeName, claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	reg.Add(storeName, stubStore)

	intent := "rw"
	okClaimID := shared.UUID(uuid.New())
	failClaimID := shared.UUID(uuid.New())
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		for _, id := range []shared.UUID{okClaimID, failClaimID} {
			if err := backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
				ID: id, LockKind: persistence.LockKindScope,
				ProducerName: &storeName, ClaimScopeData: []byte(`"durable"`), Address: []byte(`"durable-addr"`),
				Intent:             &intent,
				HolderSupervisorID: "sup-cleanup", HolderNodeID: acqNode.ID,
				ExpiresAt: time.Now().Add(10 * time.Minute),
				IsHeld:    true,
				Lifetime:  spec.ClaimLifetimeDurable,
			}, tx); err != nil {
				return err
			}
			if err := backend.ClaimHandles().Promote(ctx, id, "sup-cleanup", spec.ClaimHandleStateCommitted, tx); err != nil {
				return err
			}
		}
		return nil
	}))

	releaseErr := errors.New("store unavailable")
	stubStore.ErrorFunc = func(verb string, claimID claimproducer.ClaimID) error {
		if verb == "release" && claimID == claimproducer.ClaimID(failClaimID.String()) {
			return releaseErr
		}
		return nil
	}

	args := runtime.RunArgs{
		Persist:       backend,
		ClaimHandles:  backend.ClaimHandles(),
		StoreRegistry: reg,
		Logger:        shared.SilentLogger{},
		SupervisorID:  "sup-cleanup",
	}
	var report runtime.HeldDurableReleaseReport
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := runtime.ReleaseHeldDurableClaims(ctx, args, tx, instID, shared.SilentLogger{})
		report = r
		return err
	}))

	require.Equal(t, 2, report.Attempted)
	require.Equal(t, 1, report.Succeeded)
	require.Len(t, report.Failures, 1)
	require.Equal(t, failClaimID, report.Failures[0].ClaimHandleID)
	require.ErrorIs(t, report.Failures[0].Err, releaseErr)
	require.Equal(t, report.Attempted, report.Succeeded+len(report.Failures),
		"report invariant: Attempted must equal Succeeded + len(Failures)")

	var okRow, failRow *persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, okClaimID, tx)
		okRow = r
		return err
	}))
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, failClaimID, tx)
		failRow = r
		return err
	}))
	require.Nil(t, okRow, "a claim whose producer.Release succeeded must be dropped from the store")
	require.NotNil(t, failRow, "a claim whose producer.Release failed must be preserved for retry, not deleted")
}
