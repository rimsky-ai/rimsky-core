// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: breakpoint

// @concept: breakpoint
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

func testSelectCandidatesSkipsPausedInstances(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	store := d.Tables()
	q := d.Queue()

	activeFix := seedFixtureSet(ctx, t, d)

	pausedInstanceID := shared.UUID(uuid.New())
	pausedRunScopeID := shared.UUID(uuid.New())
	pausedNodeID := shared.UUID(uuid.New())
	var pausedFrameID shared.UUID
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:         pausedRunScopeID,
			GraphName:  spec.MainGraphName,
			InstanceID: pausedInstanceID,
		}); err != nil {
			return err
		}
		if _, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID:           pausedInstanceID,
			TemplateHash: activeFix.TemplateHash,
			Paused:       true,
		}, tx); err != nil {
			return err
		}
		if _, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID:         pausedNodeID,
			InstanceID: pausedInstanceID,
			NodeType:   "fixture-node-type",
			Executor:   "test-executor",
		}, tx); err != nil {
			return err
		}
		pausedMessageID := shared.UUID(uuid.New())
		if err := store.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID:         pausedMessageID,
			InstanceID: pausedInstanceID,
			Type:       "fixture/message",
			Sender:     "operator",
			SenderKind: "operator",
		}); err != nil {
			return err
		}
		fid, err := store.Frames().InsertFrame(ctx, pausedInstanceID, pausedMessageID, pausedRunScopeID, 600000, tx)
		if err != nil {
			return err
		}
		pausedFrameID = fid
		if _, err := store.Frames().PromoteQueuedFrameToRunning(ctx, fid, tx); err != nil {
			return err
		}
		return q.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:                 pausedNodeID,
			ExecutorName:           "test-executor",
			RequiredClaimProducers: []string{},
			EnqueuedAt:             time.Now().Add(-1 * time.Second),
			FrameID:                pausedFrameID,
			RunScopeID:             pausedRunScopeID,
		}, tx)
	}); err != nil {
		t.Fatalf("seed paused instance: %v", err)
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return q.EnqueueInTx(ctx, persistence.DispatchRequest{
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
	var sawActive, sawPaused bool
	err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors:      []string{"test-executor"},
			AcceptedClaimProducers: []string{},
			Limit:                  100,
		})
		if err != nil {
			return err
		}
		for _, c := range cands {
			switch c.NodeID {
			case activeFix.NodeID:
				sawActive = true
			case pausedNodeID:
				sawPaused = true
			}
		}
		return probeErr
	})
	if err != nil && !errors.Is(err, probeErr) {
		t.Fatalf("SelectCandidates: %v", err)
	}
	if !sawActive {
		t.Errorf("active instance's row missing from SelectCandidates")
	}
	if sawPaused {
		t.Errorf("paused instance's row leaked through SelectCandidates")
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := store.Instances().SetPaused(ctx, pausedInstanceID, false, tx)
		return err
	}); err != nil {
		t.Fatalf("SetPaused(unpause): %v", err)
	}

	sawPaused = false
	err = store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors:      []string{"test-executor"},
			AcceptedClaimProducers: []string{},
			Limit:                  100,
		})
		if err != nil {
			return err
		}
		for _, c := range cands {
			if c.NodeID == pausedNodeID {
				sawPaused = true
			}
		}
		return probeErr
	})
	if err != nil && !errors.Is(err, probeErr) {
		t.Fatalf("SelectCandidates after unpause: %v", err)
	}
	if !sawPaused {
		t.Errorf("formerly-paused instance's row missing from SelectCandidates after unpause")
	}
}
