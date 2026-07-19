// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package observability_test

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

type instanceFixture struct {
	TemplateHash   string
	InstanceID     shared.UUID
	NodeID         shared.UUID
	MainRunScopeID shared.UUID
}

func seedInstanceWithNode(t *testing.T, ctx context.Context, store persistence.Tables, tmplSpec spec.TemplateSpec) instanceFixture {
	t.Helper()
	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	nodeID := shared.UUID(uuid.New())
	mainRunScopeID := shared.UUID(uuid.New())

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
			ID:         mainRunScopeID,
			GraphName:  spec.MainGraphName,
			InstanceID: instanceID,
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
			NodeType:   tmplSpec.Nodes[0].Type,
			Executor:   tmplSpec.Nodes[0].Executor,
		}, tx)
		return err
	}); err != nil {
		t.Fatalf("seedInstanceWithNode: %v", err)
	}
	return instanceFixture{
		TemplateHash:   templateHash,
		InstanceID:     instanceID,
		NodeID:         nodeID,
		MainRunScopeID: mainRunScopeID,
	}
}

func singleNodeTemplateSpec(nodeType string) spec.TemplateSpec {
	return spec.TemplateSpec{
		Name:           "fixture-template",
		Version:        "1",
		FrameTimeoutMs: 600000,
		Nodes: []spec.TemplateNodeDef{
			{Type: nodeType, Executor: "test-executor"},
		},
	}
}

func seedFrame(t *testing.T, ctx context.Context, store persistence.Tables, instanceID, runScopeID shared.UUID, msgType string) (shared.UUID, shared.UUID) {
	t.Helper()
	msgID := shared.UUID(uuid.New())
	var frameID shared.UUID
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID:         msgID,
			InstanceID: instanceID,
			Type:       msgType,
			Sender:     "test-frame-seed",
			SenderKind: "operator",
			ReceivedAt: time.Now().UTC(),
		}); err != nil {
			return err
		}
		fid, err := store.Frames().InsertRunningFrame(ctx, instanceID, msgID, runScopeID, 600000, tx)
		if err != nil {
			return err
		}
		frameID = fid
		return nil
	}); err != nil {
		t.Fatalf("seedFrame: %v", err)
	}
	return frameID, msgID
}

func endFrame(t *testing.T, ctx context.Context, store persistence.Tables, frameID shared.UUID) {
	t.Helper()
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		transitioned, err := store.Frames().MarkFrameEnded(ctx, frameID, tx)
		if err != nil {
			return err
		}
		if !transitioned {
			t.Fatalf("endFrame: frame %s did not transition to ended", frameID)
		}
		return nil
	}); err != nil {
		t.Fatalf("endFrame: %v", err)
	}
}

func seedPendingRun(t *testing.T, ctx context.Context, d persistence.Database, nodeID, frameID, runScopeID shared.UUID) shared.UUID {
	t.Helper()
	store := d.Tables()
	q := d.Queue()
	var runID shared.UUID
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := q.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:                 nodeID,
			ExecutorName:           "test-executor",
			RequiredClaimProducers: []string{},
			EnqueuedAt:             time.Now().Add(-time.Second),
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
				runID = c.NodeRunID
				return nil
			}
		}
		t.Fatalf("seedPendingRun: candidate not surfaced for node %s", nodeID)
		return nil
	}); err != nil {
		t.Fatalf("seedPendingRun: %v", err)
	}
	return runID
}

func claimAndPromoteRun(t *testing.T, ctx context.Context, d persistence.Database, runID shared.UUID, supervisorID string) {
	t.Helper()
	store := d.Tables()
	q := d.Queue()
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		claimed, err := q.ClaimDispatchRow(ctx, tx, runID, supervisorID)
		if err != nil {
			return err
		}
		if !claimed {
			t.Fatalf("claimAndPromoteRun: run %s not claimed", runID)
		}
		promoted, err := q.PromoteClaimedToRunning(ctx, tx, runID, supervisorID)
		if err != nil {
			return err
		}
		if !promoted {
			t.Fatalf("claimAndPromoteRun: run %s not promoted", runID)
		}
		return nil
	}); err != nil {
		t.Fatalf("claimAndPromoteRun: %v", err)
	}
}

func markRunFailed(t *testing.T, ctx context.Context, store persistence.Tables, runID shared.UUID) {
	t.Helper()
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.Nodes().UpdateState(ctx, runID, cascade.NodeStateFailed, cascade.ReasonPolicyGiveUp, nil, tx)
	}); err != nil {
		t.Fatalf("markRunFailed: %v", err)
	}
}
