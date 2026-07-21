// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package conformance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func testQueueInTxAndDispatchNode(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	q := d.Queue()
	if q == nil {
		t.Fatalf("driver.Queue() returned nil")
	}

	rollbackErr := errors.New("rollback enqueue")
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := q.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:                 fix.NodeID,
			ExecutorName:           "test-executor",
			RequiredClaimProducers: []string{},
			EnqueuedAt:             time.Now().Add(-1 * time.Second),
			FrameID:                fix.FrameID,
			RunScopeID:             fix.MainRunScopeID,
		}, tx); err != nil {
			return err
		}
		return rollbackErr
	}); !errors.Is(err, rollbackErr) {
		t.Fatalf("EnqueueInTx rollback: expected rollbackErr, got %v", err)
	}

	if found := selectCandidateIDForNode(ctx, t, store, q, fix.NodeID); found != (shared.UUID{}) {
		t.Fatalf("EnqueueInTx rollback: row %v leaked through after rollback", found)
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return q.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:                 fix.NodeID,
			ExecutorName:           "test-executor",
			RequiredClaimProducers: []string{},
			EnqueuedAt:             time.Now().Add(-1 * time.Second),
			FrameID:                fix.FrameID,
			RunScopeID:             fix.MainRunScopeID,
		}, tx)
	}); err != nil {
		t.Fatalf("EnqueueInTx commit: %v", err)
	}

	nodeRunID := selectCandidateIDForNode(ctx, t, store, q, fix.NodeID)
	if nodeRunID == (shared.UUID{}) {
		t.Fatalf("EnqueueInTx commit: row not visible after commit")
	}

	missingID := uuid.New()
	gotNode, owner, err := q.GetDispatchNode(ctx, missingID)
	if err != nil {
		t.Fatalf("GetDispatchNode not_found: err: %v", err)
	}
	if owner.Kind != "not_found" {
		t.Fatalf("GetDispatchNode not_found: kind=%q want %q", owner.Kind, "not_found")
	}
	if gotNode != (shared.UUID{}) {
		t.Fatalf("GetDispatchNode not_found: nodeID=%v want zero", gotNode)
	}

	gotNode, owner, err = q.GetDispatchNode(ctx, nodeRunID)
	if err != nil {
		t.Fatalf("GetDispatchNode unclaimed: err: %v", err)
	}
	if owner.Kind != "unclaimed" {
		t.Fatalf("GetDispatchNode unclaimed: kind=%q want %q", owner.Kind, "unclaimed")
	}
	if gotNode != fix.NodeID {
		t.Fatalf("GetDispatchNode unclaimed: nodeID=%v want %v", gotNode, fix.NodeID)
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		gotNode, owner, err := q.GetDispatchNodeInTx(ctx, tx, nodeRunID)
		if err != nil {
			return err
		}
		if owner.Kind != "unclaimed" {
			t.Fatalf("GetDispatchNodeInTx unclaimed: kind=%q want %q", owner.Kind, "unclaimed")
		}
		if gotNode != fix.NodeID {
			t.Fatalf("GetDispatchNodeInTx unclaimed: nodeID=%v want %v", gotNode, fix.NodeID)
		}
		_, owner, err = q.GetDispatchNodeInTx(ctx, tx, uuid.New())
		if err != nil {
			return err
		}
		if owner.Kind != "not_found" {
			t.Fatalf("GetDispatchNodeInTx not_found: kind=%q want %q", owner.Kind, "not_found")
		}
		return nil
	}); err != nil {
		t.Fatalf("GetDispatchNodeInTx: %v", err)
	}

	supID := "queue-in-tx-supervisor"
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		ok, err := q.ClaimDispatchRow(ctx, tx, nodeRunID, supID)
		if err != nil {
			return err
		}
		if !ok {
			t.Fatalf("ClaimDispatchRow returned !ok on unclaimed row")
		}
		_, err = q.PromoteClaimedToRunning(ctx, tx, nodeRunID, supID)
		return err
	}); err != nil {
		t.Fatalf("claim tx: %v", err)
	}
	gotNode, owner, err = q.GetDispatchNode(ctx, nodeRunID)
	if err != nil {
		t.Fatalf("GetDispatchNode claimed_by: err: %v", err)
	}
	if owner.Kind != "claimed_by" {
		t.Fatalf("GetDispatchNode claimed_by: kind=%q want %q", owner.Kind, "claimed_by")
	}
	if owner.SupervisorID != supID {
		t.Fatalf("GetDispatchNode claimed_by: supervisorID=%q want %q", owner.SupervisorID, supID)
	}
	if gotNode != fix.NodeID {
		t.Fatalf("GetDispatchNode claimed_by: nodeID=%v want %v", gotNode, fix.NodeID)
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := q.RemoveForNodeInTx(ctx, fix.NodeID, fix.MainRunScopeID, supID, tx); err != nil {
			return err
		}
		return rollbackErr
	}); !errors.Is(err, rollbackErr) {
		t.Fatalf("RemoveForNodeInTx rollback: expected rollbackErr, got %v", err)
	}
	_, owner, err = q.GetDispatchNode(ctx, nodeRunID)
	if err != nil {
		t.Fatalf("GetDispatchNode after rollback: err: %v", err)
	}
	if owner.Kind != "claimed_by" || owner.SupervisorID != supID {
		t.Fatalf("RemoveForNodeInTx rollback failed: row gone or claim cleared (kind=%q, sup=%q)",
			owner.Kind, owner.SupervisorID)
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return q.RemoveForNodeInTx(ctx, fix.NodeID, fix.MainRunScopeID, "different-supervisor", tx)
	}); err != nil {
		t.Fatalf("RemoveForNodeInTx wrong sup: %v", err)
	}
	_, owner, err = q.GetDispatchNode(ctx, nodeRunID)
	if err != nil {
		t.Fatalf("GetDispatchNode after wrong-sup remove: err: %v", err)
	}
	if owner.Kind != "claimed_by" {
		t.Fatalf("RemoveForNodeInTx with wrong supervisor was not a no-op: kind=%q", owner.Kind)
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.Nodes().UpdateState(ctx, nodeRunID,
			cascade.NodeStateFresh, cascade.ReasonHandlerComplete, nil, tx); err != nil {
			return err
		}
		return q.RemoveForNodeInTx(ctx, fix.NodeID, fix.MainRunScopeID, supID, tx)
	}); err != nil {
		t.Fatalf("RemoveForNodeInTx commit: %v", err)
	}
	_, owner, err = q.GetDispatchNode(ctx, nodeRunID)
	if err != nil {
		t.Fatalf("GetDispatchNode after retire: err: %v", err)
	}
	if owner.Kind != "unclaimed" {
		t.Fatalf("RemoveForNodeInTx commit did not clear claim (expected kind=unclaimed): kind=%q", owner.Kind)
	}
	if found := selectCandidateIDForNode(ctx, t, store, q, fix.NodeID); found != (shared.UUID{}) {
		t.Fatalf("RemoveForNodeInTx commit left the retired row %s dispatchable via SelectCandidates", found)
	}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return q.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:                 fix.NodeID,
			ExecutorName:           "test-executor",
			RequiredClaimProducers: []string{},
			EnqueuedAt:             time.Now().Add(-1 * time.Second),
			FrameID:                fix.FrameID,
			RunScopeID:             fix.MainRunScopeID,
		}, tx)
	}); err != nil {
		t.Fatalf("EnqueueInTx after retire: %v", err)
	}
	found := selectCandidateIDForNode(ctx, t, store, q, fix.NodeID)
	if found == (shared.UUID{}) {
		t.Fatalf("EnqueueInTx after retire: no fresh in-flight row visible")
	}
	if found == nodeRunID {
		t.Fatalf("EnqueueInTx after retire surfaced the retired node_run_id %s again; "+
			"the retire-then-re-enqueue must produce a new row, not leave the old one dispatchable", nodeRunID)
	}
}

func selectCandidateIDForNode(ctx context.Context, t *testing.T,
	store persistence.Tables, q persistence.Queue, nodeID shared.UUID,
) shared.UUID {
	t.Helper()
	probeErr := errors.New("rollback probe")
	var found shared.UUID
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
			if c.NodeID == nodeID {
				found = c.NodeRunID
				break
			}
		}
		return probeErr
	})
	if err != nil && !errors.Is(err, probeErr) {
		t.Fatalf("selectCandidateIDForNode: %v", err)
	}
	return found
}
