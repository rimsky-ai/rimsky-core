// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: claim-lifetime
// @concept: auto-terminal
// @concept: claim-handle

package asset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

func TestDurableLifetimeE2E(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmpl := insertDeployedTemplateAsset(ctx, t, backend, node.TemplateSpec{
		Name: "durable-lifetime-e2e", Version: "1",
	})
	ck := "ck-durable-e2e"
	var inst persistence.InstanceRow
	var acqNode persistence.NodeRow
	instID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID: mainScopeID, GraphName: "main", InstanceID: instID,
		}, tx); err != nil {
			return err
		}
		i, err := backend.Instances().Create(ctx, persistence.InstanceCreateInput{TargetRoutingIdentity: "test-daemon",
			ID: instID, TemplateHash: tmpl.ID,
			InstanceKey: &ck, Params: map[string]any{},
		}, tx)
		if err != nil {
			return err
		}
		inst = i
		a, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID,
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

	frameID := seedFrameAsset(ctx, t, backend, inst.ID, mainScopeID)
	acqRunID := seedRunForNodeAsset(ctx, t, backend, d.Queue(), acqNode.ID, frameID)

	intent := "rw"
	claimHandleID := shared.UUID(uuid.New())
	prodName := producerName
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID: claimHandleID, LockKind: persistence.LockKindScope,
			ProducerName: &prodName, ClaimScopeData: []byte(`"durable"`), Address: []byte(`"durable-addr"`),
			Intent:             &intent,
			HolderSupervisorID: "sup-E2E", HolderNodeID: acqNode.ID,
			ExpiresAt: time.Now().Add(10 * time.Minute),
			IsHeld:    true,
			Lifetime:  "durable",
		}, tx); err != nil {
			return err
		}
		return backend.ClaimHolders().Insert(ctx, persistence.ClaimHolderInsertInput{
			ID: shared.UUID(uuid.New()), ClaimHandleID: claimHandleID, HolderNodeRunID: acqRunID,
		}, tx)
	}))

	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.ClaimHolders().CompleteByClaimHandleAndRun(
			ctx, claimHandleID, acqRunID, persistence.ClaimHolderStateCompleted, tx,
		)
	}))

	args := runtime.RunArgs{
		Persist:               backend,
		ClaimHandles:          backend.ClaimHandles(),
		ClaimProducerRegistry: reg,
		Logger:                shared.SilentLogger{},
		SupervisorID:          "sup-E2E",
		Clock:                 shared.SystemClock{},
	}
	var post func(context.Context)
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		pc, err := runtime.CheckAndFireResolution(ctx, args, claimHandleID, tx)
		post = pc
		return err
	}))
	if post != nil {
		post(ctx)
	}
	_, ferr := runtime.FlushProducerVerbOutbox(ctx, args)
	require.NoError(t, ferr)

	var row *persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, claimHandleID, tx)
		row = r
		return err
	}))
	require.NotNil(t, row, "durable claim must survive auto-terminal")
	require.Equal(t, spec.ClaimHandleStateCommitted, row.State,
		"durable claim must be promoted to state=committed at auto-terminal")
	require.Equal(t, spec.ClaimLifetimeDurable, row.Lifetime,
		"durable claim must carry lifetime=durable")

	var durables []persistence.ClaimHandleRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rows, err := backend.ClaimHandles().ListByInstanceAndState(
			ctx, inst.ID, spec.ClaimHandleStateCommitted, spec.ClaimLifetimeDurable, tx,
		)
		durables = rows
		return err
	}))
	require.Len(t, durables, 1)
	require.Equal(t, claimHandleID, durables[0].ID)

	var report runtime.CommittedDurableReleaseReport
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := runtime.ReleaseCommittedDurableClaims(ctx, args, inst.ID, shared.SilentLogger{}, tx)
		report = r
		return err
	}))
	require.Equal(t, 1, report.Attempted)
	require.Equal(t, 1, report.Succeeded)
	require.Empty(t, report.Failures)

	_, rferr := runtime.FlushProducerVerbOutbox(ctx, args)
	require.NoError(t, rferr)

	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.ClaimHandles().Get(ctx, claimHandleID, tx)
		row = r
		return err
	}))
	require.Nil(t, row, "ReleaseCommittedDurableClaims must drop the row")

	releaseSeen := false
	for _, c := range stubStore.Calls() {
		if c.Verb == "release" {
			releaseSeen = true
		}
	}
	require.True(t, releaseSeen, "producer.Release must fire during instance termination cleanup")
}

func insertDeployedTemplateAsset(ctx context.Context, t *testing.T, sb persistence.Tables, tmplSpec node.TemplateSpec) persistence.TemplateRow {
	t.Helper()
	sum := sha256.Sum256([]byte(tmplSpec.Name + ":" + tmplSpec.Version))
	hash := "sha256-" + hex.EncodeToString(sum[:])
	var row *persistence.TemplateRow
	require.NoError(t, sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := sb.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: hash, Spec: tmplSpec, State: persistence.TemplateStateRegistered,
		}, tx); err != nil {
			return err
		}
		if err := sb.Templates().UpdateState(ctx, hash, persistence.TemplateStateDeployed, tx); err != nil {
			return err
		}
		r, err := sb.Templates().GetByHash(ctx, hash, tx)
		row = r
		return err
	}))
	require.NotNil(t, row)
	return *row
}

func seedFrameAsset(ctx context.Context, t *testing.T, sb persistence.Tables, instanceID, rootScope shared.UUID) shared.UUID {
	t.Helper()
	var frameID shared.UUID
	require.NoError(t, sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		msgID := shared.UUID(uuid.New())
		if err := sb.Messages().Insert(ctx, persistence.EnqueueMessageRequest{
			ID:         msgID,
			InstanceID: instanceID,
			Type:       "test/seed",
			Sender:     "test",
			SenderKind: "operator",
		}, tx); err != nil {
			return err
		}
		fid, err := sb.Frames().InsertRunningFrame(ctx, instanceID, msgID, rootScope, tx)
		if err != nil {
			return err
		}
		frameID = fid
		return nil
	}))
	return frameID
}

func seedRunForNodeAsset(
	ctx context.Context, t *testing.T, sb persistence.Tables, q persistence.Queue,
	nodeID, frameID shared.UUID,
) shared.UUID {
	t.Helper()
	var out shared.UUID
	var scopeID shared.UUID
	require.NoError(t, sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		frameRow, err := sb.Frames().GetForObservability(ctx, frameID, tx)
		if err != nil {
			return err
		}
		if frameRow == nil {
			t.Fatalf("seedRunForNodeAsset: frame %s missing", frameID)
		}
		scopeID = frameRow.RootRunScopeID
		return nil
	}))
	require.NoError(t, sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := q.Enqueue(ctx, persistence.DispatchRequest{
			NodeID:                 nodeID,
			ExecutorName:           "stub",
			RequiredClaimProducers: []string{},
			EnqueuedAt:             time.Now().Add(-1 * time.Second),
			FrameID:                frameID,
			RunScopeID:             scopeID,
		}, tx); err != nil {
			return err
		}
		cands, err := q.SelectCandidates(ctx, persistence.SelectCandidatesRequest{
			Limit: 16,
		}, tx)
		if err != nil {
			return err
		}
		for _, c := range cands {
			if c.NodeID == nodeID {
				out = c.NodeRunID
				return nil
			}
		}
		t.Fatalf("seedRunForNodeAsset: candidate not surfaced for %s", nodeID)
		return nil
	}))
	return out
}
