// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

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
	"github.com/rimsky-ai/rimsky-core/test/support/pgdbtest"
)

type fanOutWriteSemanticsFixture struct {
	backend       persistence.Tables
	tmplSpec      *node.TemplateSpec
	instanceID    shared.UUID
	runScopeID    shared.UUID
	frameID       shared.UUID
	consumerNode  persistence.NodeRow
	parentClaimID shared.UUID
	supervisorID  string
	producerName  string
}

func openFanOutWriteSemanticsFixture(
	ctx context.Context, t *testing.T, namePrefix string, parentRealized claimproducer.WriteSemantics,
) fanOutWriteSemanticsFixture {
	t.Helper()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	fx := fanOutWriteSemanticsFixture{
		backend:      backend,
		instanceID:   shared.UUID(uuid.New()),
		runScopeID:   shared.UUID(uuid.New()),
		supervisorID: "sup-" + namePrefix,
		producerName: namePrefix + "-producer",
	}
	fx.tmplSpec = &node.TemplateSpec{
		Name:    namePrefix + "-tmpl",
		Version: "1",
		Nodes: []node.TemplateNodeDef{
			{
				Type:     "producer",
				Executor: "stub",
				ClaimProducers: []spec.NodeClaimProducerRef{
					{Name: fx.producerName, Selector: "sel", Intent: "rw"},
				},
			},
			{
				Type:     "fan-out-consumer",
				Executor: "stub",
				Holds:    map[string]spec.HoldsBinding{fx.producerName: {From: "producer"}},
				FanOut: &spec.FanOutSpec{
					Claim:            fx.producerName,
					PartitionRequest: `"all"`,
				},
			},
		},
	}
	sum := sha256.Sum256([]byte(fx.tmplSpec.Name + ":" + fx.tmplSpec.Version))
	tmplHash := "sha256-" + hex.EncodeToString(sum[:])
	instanceKey := "ck-" + namePrefix

	var producerNode persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: tmplHash, Spec: *fx.tmplSpec, State: persistence.TemplateStateRegistered,
		}, tx); err != nil {
			return err
		}
		if err := backend.Templates().UpdateState(ctx, tmplHash, persistence.TemplateStateDeployed, tx); err != nil {
			return err
		}
		if err := backend.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID: fx.runScopeID, GraphName: "main", InstanceID: fx.instanceID,
		}, tx); err != nil {
			return err
		}
		if _, err := backend.Instances().Create(ctx, persistence.InstanceCreateInput{
			TargetRoutingIdentity: "test-agent",
			ID:                    fx.instanceID, TemplateHash: tmplHash, InstanceKey: &instanceKey, Params: map[string]any{},
		}, tx); err != nil {
			return err
		}
		p, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: fx.instanceID, NodeType: "producer", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		producerNode = p
		c, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: fx.instanceID, NodeType: "fan-out-consumer", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		fx.consumerNode = c
		return nil
	}))

	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		msgID := shared.UUID(uuid.New())
		if err := backend.Messages().Insert(ctx, persistence.EnqueueMessageRequest{
			ID: msgID, InstanceID: fx.instanceID, Type: "test/seed", Sender: "test", SenderKind: "operator",
		}, tx); err != nil {
			return err
		}
		fid, err := backend.Frames().InsertRunningFrame(ctx, fx.instanceID, msgID, fx.runScopeID, tx)
		fx.frameID = fid
		return err
	}))

	fx.parentClaimID = shared.UUID(uuid.New())
	producerRef := fx.producerName
	intent := "rw"
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                     fx.parentClaimID,
			LockKind:               persistence.LockKindScope,
			ProducerName:           &producerRef,
			ClaimScopeData:         []byte(`"parent-scope"`),
			Address:                []byte(`"addr"`),
			Intent:                 &intent,
			RealizedWriteSemantics: string(parentRealized),
			HolderSupervisorID:     fx.supervisorID,
			HolderNodeID:           producerNode.ID,
			ExpiresAt:              time.Now().Add(time.Hour),
			FrameID:                &fx.frameID,
		}, tx)
	}))
	return fx
}

func (fx fanOutWriteSemanticsFixture) runArgs(splitScope []byte) RunArgs {
	store := storetest.NewFake(fx.producerName, claimproducer.Capabilities{
		WriteSemanticsAllowed: []claimproducer.WriteSemantics{
			claimproducer.WriteSemanticsSync, claimproducer.WriteSemanticsStagedAsync,
		},
		SupportsSplitScope: true,
	})
	store.SplitClaimScopeFunc = func(claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
		return claimproducer.SplitClaimScopeResponse{
			SubClaimScopes: []claimproducer.SubClaimScopeDescriptor{
				{PartitionKey: "alpha", ClaimScopeData: splitScope},
			},
		}, nil
	}
	reg := locks.NewRegistry()
	reg.Add(fx.producerName, store)
	return RunArgs{
		Persist:               fx.backend,
		ClaimHandles:          fx.backend.ClaimHandles(),
		ClaimProducerRegistry: reg,
		Logger:                shared.SilentLogger{},
		Clock:                 shared.SystemClock{},
		SupervisorID:          fx.supervisorID,
	}
}

func (fx fanOutWriteSemanticsFixture) seedConsumerRun(ctx context.Context, t *testing.T) shared.UUID {
	t.Helper()
	runID := shared.UUID(uuid.New())
	require.NoError(t, fx.backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return fx.backend.NodeRunTree().CreateRootNodeRun(ctx, persistence.CreateRootNodeRunInput{
			NodeRunID:    runID,
			NodeID:       fx.consumerNode.ID,
			FrameID:      fx.frameID,
			RunScopeID:   fx.runScopeID,
			ExecutorName: "stub",
		}, tx)
	}))
	return runID
}

func (fx fanOutWriteSemanticsFixture) realizedWriteSemanticsOf(
	ctx context.Context, t *testing.T, claimHandleID shared.UUID,
) string {
	t.Helper()
	var realized string
	require.NoError(t, fx.backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		row, err := fx.backend.ClaimHandles().Get(ctx, claimHandleID, tx)
		if err != nil {
			return err
		}
		require.NotNil(t, row)
		realized = row.RealizedWriteSemantics
		return nil
	}))
	return realized
}

// @concept: claim-tree
// @concept: fan-out
func TestAcquireFanOutIfDeclared_SubClaimInheritsParentRealizedWriteSemantics_HoldsPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := openFanOutWriteSemanticsFixture(ctx, t, "fanout-ws-holds", claimproducer.WriteSemanticsSync)

	args := fx.runArgs([]byte(`"sub-scope-holds"`))
	consumerDef := LookupNodeDef(fx.tmplSpec, "fan-out-consumer")
	require.NotNil(t, consumerDef)

	out := &acquisition{InstanceID: fx.instanceID, RunScopeID: fx.runScopeID}
	cand := persistence.Candidate{
		FrameID:   fx.frameID,
		NodeID:    fx.consumerNode.ID,
		NodeRunID: fx.seedConsumerRun(ctx, t),
	}
	require.NoError(t, fx.backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return acquireFanOutIfDeclared(ctx, args, fx.instanceID, out, cand, consumerDef, nil, 30*time.Second, tx)
	}))
	require.Len(t, out.SubClaims, 1)

	parentRealized := fx.realizedWriteSemanticsOf(ctx, t, fx.parentClaimID)
	require.Equal(t, string(claimproducer.WriteSemanticsSync), parentRealized)
	require.Equal(t, parentRealized, fx.realizedWriteSemanticsOf(ctx, t, out.SubClaims[0].ClaimHandleID),
		"the holds-bound fan-out path must thread the parent claim-handle row's realized write semantics "+
			"onto every sub-claim it inserts")
}

// @concept: claim-tree
// @concept: fan-out
func TestAcquireFanOutIfDeclared_SubClaimInheritsParentRealizedWriteSemantics_AcquiredLockPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := openFanOutWriteSemanticsFixture(ctx, t, "fanout-ws-lock", claimproducer.WriteSemanticsSync)

	args := fx.runArgs([]byte(`"sub-scope-lock"`))
	consumerDef := LookupNodeDef(fx.tmplSpec, "fan-out-consumer")
	require.NotNil(t, consumerDef)

	acquiredLocks := []AcquiredLock{{
		Spec: claimproducer.ClaimSpec{
			ProducerName: fx.producerName,
			Selector:     "sel",
			Intent:       claimproducer.IntentReadWrite,
			Alias:        fx.producerName,
		},
		Alias:         fx.producerName,
		ClaimHandleID: fx.parentClaimID,
		ClaimResult: claimproducer.ClaimResult{
			ClaimScope:             []byte(`"parent-scope"`),
			Address:                []byte(`"addr"`),
			RealizedWriteSemantics: claimproducer.WriteSemanticsStagedAsync,
		},
	}}

	out := &acquisition{InstanceID: fx.instanceID, RunScopeID: fx.runScopeID}
	cand := persistence.Candidate{
		FrameID:   fx.frameID,
		NodeID:    fx.consumerNode.ID,
		NodeRunID: fx.seedConsumerRun(ctx, t),
	}
	require.NoError(t, fx.backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return acquireFanOutIfDeclared(ctx, args, fx.instanceID, out, cand, consumerDef, acquiredLocks, 30*time.Second, tx)
	}))
	require.Len(t, out.SubClaims, 1)

	require.Equal(t, string(claimproducer.WriteSemanticsStagedAsync),
		fx.realizedWriteSemanticsOf(ctx, t, out.SubClaims[0].ClaimHandleID),
		"when the fan-out parent resolves through an acquired lock, the sub-claim must carry that "+
			"open's realized write semantics, not the claim-handle row's stale value")
}
