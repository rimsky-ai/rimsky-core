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

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/pgdbtest"
)

func seedCoHolderInstance(
	ctx context.Context, t *testing.T, backend persistence.Tables, instanceKey string,
) (inst persistence.InstanceRow, producerNode, coHolderNode persistence.NodeRow, mainScopeID shared.UUID) {
	t.Helper()
	tmplSpec := coHolderTemplateSpec(instanceKey)
	sum := sha256.Sum256([]byte(tmplSpec.Name + ":" + tmplSpec.Version))
	tmplHash := "sha256-" + hex.EncodeToString(sum[:])
	ck := instanceKey
	instID := shared.UUID(uuid.New())
	mainScopeID = shared.UUID(uuid.New())
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: tmplHash, Spec: *tmplSpec, State: persistence.TemplateStateRegistered,
		}, tx); err != nil {
			return err
		}
		if err := backend.Templates().UpdateState(ctx, tmplHash, persistence.TemplateStateDeployed, tx); err != nil {
			return err
		}
		if err := backend.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID: mainScopeID, GraphName: "main", InstanceID: instID,
		}, tx); err != nil {
			return err
		}
		i, err := backend.Instances().Create(ctx, persistence.InstanceCreateInput{TargetRoutingIdentity: "test-agent",
			ID: instID, TemplateHash: tmplHash, InstanceKey: &ck, Params: map[string]any{},
		}, tx)
		if err != nil {
			return err
		}
		inst = i
		p, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "producer", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		producerNode = p
		c, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "co-holder", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		coHolderNode = c
		return nil
	}))
	return inst, producerNode, coHolderNode, mainScopeID
}

func seedCoHolderFrame(ctx context.Context, t *testing.T, backend persistence.Tables, instanceID, mainScopeID shared.UUID) shared.UUID {
	t.Helper()
	var frameID shared.UUID
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		msgID := shared.UUID(uuid.New())
		if err := backend.Messages().Insert(ctx, persistence.EnqueueMessageRequest{
			ID: msgID, InstanceID: instanceID, Type: "test/seed", Sender: "test", SenderKind: "operator",
		}, tx); err != nil {
			return err
		}
		fid, err := backend.Frames().InsertRunningFrame(ctx, instanceID, msgID, mainScopeID, tx)
		if err != nil {
			return err
		}
		frameID = fid
		return nil
	}))
	return frameID
}

func coHolderTemplateSpec(instanceKey string) *node.TemplateSpec {
	return &node.TemplateSpec{
		Name:    "coholder-tmpl-" + instanceKey,
		Version: "1",
		Nodes: []node.TemplateNodeDef{
			{
				Type:     "producer",
				Executor: "stub",
				ClaimProducers: []spec.NodeClaimProducerRef{
					{Name: "store-x", Selector: "sel", Intent: "rw"},
				},
			},
			{
				Type:     "co-holder",
				Executor: "stub",
				Holds:    map[string]spec.HoldsBinding{"store-x": {From: "producer"}},
			},
		},
		Graphs: []spec.GraphSpec{{
			Name: spec.MainGraphName,
			Nodes: []node.TemplateNodeDef{
				{
					Type:     "producer",
					Executor: "stub",
					ClaimProducers: []spec.NodeClaimProducerRef{
						{Name: "store-x", Selector: "sel", Intent: "rw"},
					},
				},
				{
					Type:     "co-holder",
					Executor: "stub",
					Holds:    map[string]spec.HoldsBinding{"store-x": {From: "producer"}},
				},
			},
		}},
	}
}

func insertActiveClaimForProducer(
	ctx context.Context, t *testing.T, backend persistence.Tables,
	producerNodeID, frameID shared.UUID,
) shared.UUID {
	t.Helper()
	producerName := "store-x"
	intent := "rw"
	claimID := shared.UUID(uuid.New())
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 claimID,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &producerName,
			ClaimScopeData:     []byte(`"scope"`),
			Address:            []byte(`"addr"`),
			Intent:             &intent,
			HolderSupervisorID: "sup-coholder-test",
			HolderNodeID:       producerNodeID,
			ExpiresAt:          time.Now().Add(time.Hour),
			FrameID:            &frameID,
		}, tx)
	}))
	return claimID
}

// @concept: claim-co-holdership
func TestInsertCoHolderClaimHoldersAtAcquire_HappyPathInsertsRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	inst, producerNode, coHolderNode, mainScopeID := seedCoHolderInstance(ctx, t, backend, "ck-happy")
	frameID := seedCoHolderFrame(ctx, t, backend, inst.ID, mainScopeID)
	claimID := insertActiveClaimForProducer(ctx, t, backend, producerNode.ID, frameID)

	tmplSpec := coHolderTemplateSpec("ck-happy")
	coHolderDef := LookupNodeDef(tmplSpec, "co-holder")
	require.NotNil(t, coHolderDef)

	coHolderRunID := shared.UUID(uuid.New())
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return backend.NodeRunTree().CreateRootNodeRun(ctx, persistence.CreateRootNodeRunInput{
			NodeRunID:    coHolderRunID,
			NodeID:       coHolderNode.ID,
			FrameID:      frameID,
			RunScopeID:   mainScopeID,
			ExecutorName: "stub",
		}, tx)
	}))

	args := RunArgs{Persist: backend, ClaimHandles: backend.ClaimHandles(), Logger: shared.SilentLogger{}, SupervisorID: "sup-coholder-test"}
	cand := persistence.Candidate{
		NodeID:    coHolderNode.ID,
		NodeRunID: coHolderRunID,
		NodeType:  "co-holder",
		FrameID:   frameID,
	}

	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return insertCoHolderClaimHoldersAtAcquire(ctx, args, cand, coHolderDef, tmplSpec, tx)
	}))

	var holders []persistence.ClaimHolderRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		h, err := backend.ClaimHolders().ListByClaimHandleID(ctx, claimID, tx)
		holders = h
		return err
	}))
	require.Len(t, holders, 1, "the co-holder row must be inserted unconditionally on a resolvable holds: binding")
	require.Equal(t, cand.NodeRunID, holders[0].HolderNodeRunID)
}

// @concept: claim-co-holdership
func TestInsertCoHolderClaimHoldersAtAcquire_UpstreamNodeMissingFailsLoud(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	inst, _, coHolderNode, mainScopeID := seedCoHolderInstance(ctx, t, backend, "ck-missing-upstream")
	frameID := seedCoHolderFrame(ctx, t, backend, inst.ID, mainScopeID)

	tmplSpec := coHolderTemplateSpec("ck-missing-upstream")
	coHolderDef := LookupNodeDef(tmplSpec, "co-holder")
	require.NotNil(t, coHolderDef)
	broken := *coHolderDef
	broken.Holds = map[string]spec.HoldsBinding{"store-x": {From: "nonexistent-producer-type"}}

	args := RunArgs{Persist: backend, ClaimHandles: backend.ClaimHandles(), Logger: shared.SilentLogger{}, SupervisorID: "sup-coholder-test"}
	cand := persistence.Candidate{
		NodeID:    coHolderNode.ID,
		NodeRunID: shared.UUID(uuid.New()),
		NodeType:  "co-holder",
		FrameID:   frameID,
	}

	err := backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return insertCoHolderClaimHoldersAtAcquire(ctx, args, cand, &broken, tmplSpec, tx)
	})
	require.Error(t, err,
		"an unresolvable holds: upstream must fail the acquisition, not silently warn-and-continue "+
			"leaving the co-holder row missing and the handle's auto-terminal stalled forever")
}

// @concept: claim-co-holdership
func TestInsertCoHolderClaimHoldersAtAcquire_NoActiveClaimInCurrentFrameFailsLoud(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	inst, producerNode, coHolderNode, mainScopeID := seedCoHolderInstance(ctx, t, backend, "ck-wrong-frame")
	priorFrameID := seedCoHolderFrame(ctx, t, backend, inst.ID, mainScopeID)
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := backend.Frames().MarkFrameEnded(ctx, priorFrameID, tx)
		return err
	}))
	currentFrameID := seedCoHolderFrame(ctx, t, backend, inst.ID, mainScopeID)
	require.NotEqual(t, priorFrameID, currentFrameID)

	priorClaimID := insertActiveClaimForProducer(ctx, t, backend, producerNode.ID, priorFrameID)

	tmplSpec := coHolderTemplateSpec("ck-wrong-frame")
	coHolderDef := LookupNodeDef(tmplSpec, "co-holder")
	require.NotNil(t, coHolderDef)

	args := RunArgs{Persist: backend, ClaimHandles: backend.ClaimHandles(), Logger: shared.SilentLogger{}, SupervisorID: "sup-coholder-test"}
	cand := persistence.Candidate{
		NodeID:    coHolderNode.ID,
		NodeRunID: shared.UUID(uuid.New()),
		NodeType:  "co-holder",
		FrameID:   currentFrameID,
	}

	err := backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return insertCoHolderClaimHoldersAtAcquire(ctx, args, cand, coHolderDef, tmplSpec, tx)
	})
	require.NoError(t, err,
		"a frame-N co-holder must not bind against a non-durable frame-N-1 handle, but the acquire must not "+
			"fail loud either: a loud acquire error can never succeed and spins the run stale forever. The "+
			"missing in-frame hold surfaces downstream as template_resolution_failed via attribute resolution, "+
			"which the node's error policy settles (give_up).")

	var holders []persistence.ClaimHolderRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		h, err := backend.ClaimHolders().ListByClaimHandleID(ctx, priorClaimID, tx)
		holders = h
		return err
	}))
	require.Empty(t, holders,
		"frame isolation: the frame-N co-holder must NOT be bound to the frame-N-1 (non-durable) handle — "+
			"lookupClaimHandleForAlias filters a non-durable handle by frame, not just newest ClaimedAt")
}
