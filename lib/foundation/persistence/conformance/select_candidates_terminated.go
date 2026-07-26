// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: instance
package conformance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func testSelectCandidatesSkipsTerminatedInstances(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	store := d.Tables()
	q := d.Queue()

	activeFix := seedFixtureSet(ctx, t, d)

	terminatedInstanceID := shared.UUID(uuid.New())
	terminatedRunScopeID := shared.UUID(uuid.New())
	terminatedNodeID := shared.UUID(uuid.New())
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID:         terminatedRunScopeID,
			GraphName:  spec.MainGraphName,
			InstanceID: terminatedInstanceID,
		}, tx); err != nil {
			return err
		}
		if _, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{TargetRoutingIdentity: "test-agent",
			ID:           terminatedInstanceID,
			TemplateHash: activeFix.TemplateHash,
		}, tx); err != nil {
			return err
		}
		if _, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID:         terminatedNodeID,
			InstanceID: terminatedInstanceID,
			NodeType:   "fixture-node-type",
			Executor:   "test-executor",
		}, tx); err != nil {
			return err
		}
		terminatedMessageID := shared.UUID(uuid.New())
		if err := store.Messages().Insert(ctx, persistence.EnqueueMessageRequest{
			ID:         terminatedMessageID,
			InstanceID: terminatedInstanceID,
			Type:       "fixture/message",
			Sender:     "operator",
			SenderKind: "operator",
		}, tx); err != nil {
			return err
		}
		frameID, err := store.Frames().InsertRunningFrame(ctx, terminatedInstanceID, terminatedMessageID, terminatedRunScopeID, tx)
		if err != nil {
			return err
		}
		if err := q.Enqueue(ctx, persistence.DispatchRequest{
			NodeID:                 terminatedNodeID,
			ExecutorName:           "test-executor",
			RequiredClaimProducers: []string{},
			EnqueuedAt:             time.Now().Add(-1 * time.Second),
			FrameID:                frameID,
			RunScopeID:             terminatedRunScopeID,
		}, tx); err != nil {
			return err
		}
		return store.Instances().MarkTerminated(ctx, terminatedInstanceID, tx)
	}); err != nil {
		t.Fatalf("seed terminated instance: %v", err)
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return q.Enqueue(ctx, persistence.DispatchRequest{
			NodeID:                 activeFix.NodeID,
			ExecutorName:           "test-executor",
			RequiredClaimProducers: []string{},
			EnqueuedAt:             time.Now().Add(-1 * time.Second),
			FrameID:                activeFix.FrameID,
			RunScopeID:             activeFix.MainRunScopeID,
		}, tx)
	}); err != nil {
		t.Fatalf("enqueue active row: %v", err)
	}

	probeErr := errors.New("rollback probe")
	var sawActive, sawTerminated bool
	err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		cands, err := q.SelectCandidates(ctx, persistence.SelectCandidatesRequest{
			Limit: 100,
		}, tx)
		if err != nil {
			return err
		}
		for _, c := range cands {
			switch c.NodeID {
			case activeFix.NodeID:
				sawActive = true
			case terminatedNodeID:
				sawTerminated = true
			}
		}
		return probeErr
	})
	if err != nil && !errors.Is(err, probeErr) {
		t.Fatalf("SelectCandidates: %v", err)
	}
	if !sawActive {
		t.Errorf("non-terminated instance's stale row missing from SelectCandidates")
	}
	if sawTerminated {
		t.Errorf("terminated instance's stale row leaked through SelectCandidates")
	}
}
