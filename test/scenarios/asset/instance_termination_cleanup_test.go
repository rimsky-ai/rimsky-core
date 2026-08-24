// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
	"github.com/rimsky-ai/rimsky-core/test/support/pgdbtest"
)

func TestInstanceTerminationCleanup_PreservesFailedReleaseForRetry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplateAsset(ctx, t, backend, node.TemplateSpec{
		Name: "instance-termination-cleanup", Version: "1",
	})
	instID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	ck := "ck-instance-termination-cleanup"
	var acqNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID: mainScopeID, GraphName: "main", InstanceID: instID,
		}, tx); err != nil {
			return err
		}
		if _, err := backend.Instances().Create(ctx, persistence.InstanceCreateInput{TargetRoutingIdentity: "test-daemon",
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
	producerName := "workspace"
	stubStore := storetest.NewFake(producerName, claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	reg.Add(producerName, stubStore)

	intent := "rw"
	okClaimID := shared.UUID(uuid.New())
	failClaimID := shared.UUID(uuid.New())
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		for _, id := range []shared.UUID{okClaimID, failClaimID} {
			if err := backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
				ID: id, LockKind: persistence.LockKindScope,
				ProducerName: &producerName, ClaimScopeData: []byte(`"durable-` + id.String() + `"`), Address: []byte(`"durable-addr"`),
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
	producerDown := true
	stubStore.ErrorFunc = func(verb string, claimID claimproducer.ClaimID) error {
		if producerDown && verb == "release" && claimID == claimproducer.ClaimID(failClaimID.String()) {
			return releaseErr
		}
		return nil
	}

	clock := shared.NewControllableClock(time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC))
	args := runtime.RunArgs{
		Persist:               backend,
		ClaimHandles:          backend.ClaimHandles(),
		ClaimProducerRegistry: reg,
		Logger:                shared.SilentLogger{},
		SupervisorID:          "sup-cleanup",
		Clock:                 clock,
	}
	var report runtime.CommittedDurableReleaseReport
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := runtime.ReleaseCommittedDurableClaims(ctx, args, instID, shared.SilentLogger{}, tx)
		report = r
		return err
	}))

	require.Equal(t, 2, report.Attempted)
	require.Equal(t, 2, report.Succeeded,
		"the disposition is recorded durably for every claim; delivery is the outbox's job")
	require.Empty(t, report.Failures)

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
	require.Nil(t, okRow, "released claim rows are dropped at decision time; the outbox row carries delivery")
	require.Nil(t, failRow, "released claim rows are dropped at decision time; the outbox row carries delivery")

	_, ferr := runtime.FlushProducerVerbOutbox(ctx, args)
	require.NoError(t, ferr)
	require.Equal(t, 1, countVerbCalls(stubStore, okClaimID, "release"),
		"the reachable release must deliver on the first flush")
	require.Equal(t, 1, countVerbCalls(stubStore, failClaimID, "release"),
		"the unreachable release must have been attempted once")

	outbox := runtime.ProducerVerbOutboxOf(args)
	require.NotNil(t, outbox)
	pending, err := outbox.ListAll(ctx, nil)
	require.NoError(t, err)
	require.Len(t, pending, 1, "the undelivered release must survive as an outbox row for retry")
	require.Equal(t, failClaimID, pending[0].ClaimHandleID)
	require.Equal(t, 1, pending[0].AttemptCount)

	producerDown = false
	clock.Advance(time.Minute)
	flushed, err := runtime.FlushProducerVerbOutbox(ctx, args)
	require.NoError(t, err)
	require.Equal(t, 1, flushed, "a recovered producer must receive the queued release")
	pending, err = outbox.ListAll(ctx, nil)
	require.NoError(t, err)
	require.Empty(t, pending)
}

type deleteResolvedFailingClaimHandles struct {
	persistence.ClaimHandleTable
	failID shared.UUID
	err    error
}

func (f deleteResolvedFailingClaimHandles) DeleteResolved(ctx context.Context, id shared.UUID, tx persistence.Tx) error {
	if id == f.failID {
		return f.err
	}
	return f.ClaimHandleTable.DeleteResolved(ctx, id, tx)
}

func TestInstanceTerminationCleanup_DeleteResolvedFailureAbortsWithoutOrphaningTheOutboxRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplateAsset(ctx, t, backend, node.TemplateSpec{
		Name: "instance-termination-cleanup-delete-fail", Version: "1",
	})
	instID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	ck := "ck-instance-termination-cleanup-delete-fail"
	var acqNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID: mainScopeID, GraphName: "main", InstanceID: instID,
		}, tx); err != nil {
			return err
		}
		if _, err := backend.Instances().Create(ctx, persistence.InstanceCreateInput{TargetRoutingIdentity: "test-daemon",
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
	producerName := "workspace"
	stubStore := storetest.NewFake(producerName, claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync},
	})
	reg.Add(producerName, stubStore)

	intent := "rw"
	claimID := shared.UUID(uuid.New())
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID: claimID, LockKind: persistence.LockKindScope,
			ProducerName: &producerName, ClaimScopeData: []byte(`"durable-` + claimID.String() + `"`), Address: []byte(`"durable-addr"`),
			Intent:             &intent,
			HolderSupervisorID: "sup-cleanup-delfail", HolderNodeID: acqNode.ID,
			ExpiresAt: time.Now().Add(10 * time.Minute),
			IsHeld:    true,
			Lifetime:  spec.ClaimLifetimeDurable,
		}, tx); err != nil {
			return err
		}
		return backend.ClaimHandles().Promote(ctx, claimID, "sup-cleanup-delfail", spec.ClaimHandleStateCommitted, tx)
	}))

	deleteErr := errors.New("simulated delete-resolved failure")
	failingHandles := deleteResolvedFailingClaimHandles{
		ClaimHandleTable: backend.ClaimHandles(),
		failID:           claimID,
		err:              deleteErr,
	}

	clock := shared.NewControllableClock(time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC))
	args := runtime.RunArgs{
		Persist:               backend,
		ClaimHandles:          failingHandles,
		ClaimProducerRegistry: reg,
		Logger:                shared.SilentLogger{},
		SupervisorID:          "sup-cleanup-delfail",
		Clock:                 clock,
	}
	txErr := backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := runtime.ReleaseCommittedDurableClaims(ctx, args, instID, shared.SilentLogger{}, tx)
		return err
	})
	require.Error(t, txErr)
	require.ErrorIs(t, txErr, deleteErr)

	var row *persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, claimID, tx)
		row = r
		return err
	}))
	require.NotNil(t, row, "the claim_handle row must survive when its deletion fails, matching the rolled-back outbox enqueue")

	outbox := runtime.ProducerVerbOutboxOf(args)
	require.NotNil(t, outbox)
	pending, err := outbox.ListAll(ctx, nil)
	require.NoError(t, err)
	require.Empty(t, pending, "the outbox enqueue for this claim must have rolled back alongside the failed delete, not been left as an orphaned row")
}

func countVerbCalls(f *storetest.Fake, claimID shared.UUID, verb string) int {
	n := 0
	for _, c := range f.Calls() {
		if c.ClaimID == claimproducer.ClaimID(claimID.String()) && c.Verb == verb {
			n++
		}
	}
	return n
}
