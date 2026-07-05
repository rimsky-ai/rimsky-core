// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func seedMainRunScopeForInstance(
	ctx context.Context, t *testing.T, tx persistence.Tx, store persistence.Tables,
	instanceID shared.UUID,
) shared.UUID {
	t.Helper()
	id := shared.UUID(uuid.New())
	if err := store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
		ID:         id,
		GraphName:  spec.MainGraphName,
		InstanceID: instanceID,
	}); err != nil {
		t.Fatalf("seedMainRunScopeForInstance: %v", err)
	}
	return id
}

type fixtureSet struct {
	TemplateHash   string
	InstanceID     shared.UUID
	NodeID         shared.UUID
	FrameID        shared.UUID
	MainRunScopeID shared.UUID
	MessageID      shared.UUID
}

func seedFixtureSet(ctx context.Context, t *testing.T, d persistence.Database) fixtureSet {
	t.Helper()
	store := d.Tables()
	if store == nil {
		t.Fatalf("seedFixtureSet: driver.Tables() returned nil")
	}

	templateHash := "sha256-" + uuid.NewString()
	instanceID := uuid.New()
	nodeID := uuid.New()
	frameID := uuid.New()
	mainRunScopeID := uuid.New()

	tmplSpec := spec.TemplateSpec{
		Name:           "conformance-fixture",
		Version:        "1",
		FrameTimeoutMs: 600000,
		Nodes: []spec.TemplateNodeDef{
			{Type: "fixture-node-type", Executor: "test-executor"},
		},
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID:     templateHash,
			Spec:   tmplSpec,
			State:  persistence.TemplateStateRegistered,
			Source: "direct",
		}, tx); err != nil {
			return err
		}
		if err := store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:           shared.UUID(mainRunScopeID),
			GraphName:    spec.MainGraphName,
			InstanceID:   shared.UUID(instanceID),
			PartitionKey: "",
		}); err != nil {
			return err
		}
		if _, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID:           instanceID,
			TemplateHash: templateHash,
		}, tx); err != nil {
			return err
		}
		_, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID:         nodeID,
			InstanceID: instanceID,
			NodeType:   "fixture-node-type",
			Executor:   "test-executor",
		}, tx)
		return err
	}); err != nil {
		t.Fatalf("seedFixtureSet: template/instance/node create: %v", err)
	}

	messageID := uuid.New()
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID:         shared.UUID(messageID),
			InstanceID: shared.UUID(instanceID),
			Type:       "fixture/message",
			Sender:     "operator",
			SenderKind: "operator",
		}); err != nil {
			return err
		}
		fid, err := store.Frames().InsertRunningFrame(ctx, instanceID, shared.UUID(messageID), shared.UUID(mainRunScopeID), 600000, tx)
		if err != nil {
			return err
		}
		frameID = fid
		return nil
	}); err != nil {
		t.Fatalf("seedFixtureSet: frame enqueue/promote: %v", err)
	}

	return fixtureSet{
		TemplateHash:   templateHash,
		InstanceID:     instanceID,
		NodeID:         nodeID,
		FrameID:        frameID,
		MainRunScopeID: mainRunScopeID,
		MessageID:      shared.UUID(messageID),
	}
}

// @concept: run-scope
func seedConformanceRunForNode(
	ctx context.Context, t *testing.T, d persistence.Database,
	nodeID, frameID shared.UUID,
) shared.UUID {
	t.Helper()
	store := d.Tables()
	q := d.Queue()

	var runScopeID shared.UUID
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		nodeRow, err := store.Nodes().Get(ctx, nodeID, tx)
		if err != nil {
			return err
		}
		if nodeRow == nil {
			t.Fatalf("seedConformanceRunForNode: node %s not found", nodeID)
		}
		frameRow, err := store.Frames().GetForObservability(ctx, frameID, tx)
		if err != nil {
			return err
		}
		if frameRow == nil {
			t.Fatalf("seedConformanceRunForNode: frame %s not found", frameID)
		}
		runScopeID = frameRow.RootRunScopeID
		return nil
	}); err != nil {
		t.Fatalf("seedConformanceRunForNode: resolve run scope: %v", err)
	}

	var runID shared.UUID
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := q.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:                 nodeID,
			ExecutorName:           "test-executor",
			RequiredClaimProducers: []string{},
			EnqueuedAt:             time.Now().Add(-1 * time.Second),
			FrameID:                frameID,
			RunScopeID:             runScopeID,
		}, tx); err != nil {
			return err
		}
		cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors:      []string{"test-executor"},
			AcceptedClaimProducers: []string{},
			Limit:                  16,
		})
		if err != nil {
			return err
		}
		for _, c := range cands {
			if c.NodeID == nodeID {
				runID = c.DispatchID
				return nil
			}
		}
		t.Fatalf("seedConformanceRunForNode: candidate not surfaced for %s", nodeID)
		return nil
	}); err != nil {
		t.Fatalf("seedConformanceRunForNode: %v", err)
	}
	return runID
}

// @concept: run-scope
func seedConformanceRunForScope(
	ctx context.Context, t *testing.T, d persistence.Database,
	nodeID, frameID, runScopeID shared.UUID,
) shared.UUID {
	t.Helper()
	store := d.Tables()
	q := d.Queue()

	var runID shared.UUID
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := q.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:                 nodeID,
			ExecutorName:           "test-executor",
			RequiredClaimProducers: []string{},
			EnqueuedAt:             time.Now().Add(-1 * time.Second),
			FrameID:                frameID,
			RunScopeID:             runScopeID,
		}, tx); err != nil {
			return err
		}
		id, found, err := q.GetInFlightRunForNode(ctx, tx, nodeID, runScopeID)
		if err != nil {
			return err
		}
		if !found {
			t.Fatalf("seedConformanceRunForScope: in-flight not surfaced for %s in %s", nodeID, runScopeID)
		}
		runID = id
		return nil
	}); err != nil {
		t.Fatalf("seedConformanceRunForScope: %v", err)
	}
	return runID
}

func inTx(ctx context.Context, store persistence.Tables, fn func(tx persistence.Tx) error) error {
	return store.Transaction(ctx, func(_ context.Context, tx persistence.Tx) error {
		return fn(tx)
	})
}

// @concept: node-run
func forceRunStateToFresh(
	ctx context.Context, tx persistence.Tx, store persistence.Tables, runID shared.UUID,
) error {
	row, err := store.RunTree().GetByID(ctx, tx, runID)
	if err != nil || row == nil {
		return err
	}
	switch row.State {
	case cascade.NodeStateFresh, cascade.NodeStateFailed:
		return nil
	case cascade.NodeStateStale:
		if err := store.Nodes().UpdateState(ctx, row.NodeID, row.RunScopeID,
			cascade.NodeStateRunning, cascade.ReasonDispatchClaimed, nil, tx); err != nil {
			return err
		}
		return store.Nodes().UpdateState(ctx, row.NodeID, row.RunScopeID,
			cascade.NodeStateFresh, cascade.ReasonHandlerComplete, nil, tx)
	case cascade.NodeStateRunning:
		return store.Nodes().UpdateState(ctx, row.NodeID, row.RunScopeID,
			cascade.NodeStateFresh, cascade.ReasonHandlerComplete, nil, tx)
	case cascade.NodeStateHeld:
		return store.Nodes().UpdateState(ctx, row.NodeID, row.RunScopeID,
			cascade.NodeStateFresh, cascade.ReasonAutoTerminalCommit, nil, tx)
	}
	return nil
}
