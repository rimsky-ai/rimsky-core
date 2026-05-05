// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// queue_in_tx.go — QueueInTxAndDispatchNode conformance area.
//
// Covers the tx-taking variants Queue.EnqueueInTx and Queue.RemoveForNodeInTx
// (rollback discards / commit lands), and Queue.GetDispatchNode's three
// branches (not_found, unclaimed, claimed_by(supervisorID)). These were
// added in the Tasks 23-28 pgx-removal refactor; both drivers must agree.
package conformance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/shared"
)

func testQueueInTxAndDispatchNode(t *testing.T, d persistence.Driver) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Store()
	q := d.Queue()
	if q == nil {
		t.Fatalf("driver.Queue() returned nil")
	}

	// ---- EnqueueInTx: rollback discards ----
	rollbackErr := errors.New("rollback enqueue")
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := q.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:         fix.NodeID,
			ExecutorName:   "test-executor",
			RequiredStores: []string{},
			EnqueuedAt:     time.Now().Add(-1 * time.Second),
			FrameID:        fix.FrameID,
		}, tx); err != nil {
			return err
		}
		return rollbackErr
	}); !errors.Is(err, rollbackErr) {
		t.Fatalf("EnqueueInTx rollback: expected rollbackErr, got %v", err)
	}

	// Probe: SelectCandidates inside a fresh tx must find no row for our
	// node — the rolled-back row was never visible. We roll the probe tx
	// back to release FOR UPDATE locks.
	if found := selectCandidateIDForNode(ctx, t, store, q, fix.NodeID); found != (shared.UUID{}) {
		t.Fatalf("EnqueueInTx rollback: row %v leaked through after rollback", found)
	}

	// ---- EnqueueInTx: commit lands ----
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return q.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:         fix.NodeID,
			ExecutorName:   "test-executor",
			RequiredStores: []string{},
			EnqueuedAt:     time.Now().Add(-1 * time.Second),
			FrameID:        fix.FrameID,
		}, tx)
	}); err != nil {
		t.Fatalf("EnqueueInTx commit: %v", err)
	}

	dispatchID := selectCandidateIDForNode(ctx, t, store, q, fix.NodeID)
	if dispatchID == (shared.UUID{}) {
		t.Fatalf("EnqueueInTx commit: row not visible after commit")
	}

	// ---- GetDispatchNode: not_found branch ----
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

	// ---- GetDispatchNode: unclaimed branch ----
	gotNode, owner, err = q.GetDispatchNode(ctx, dispatchID)
	if err != nil {
		t.Fatalf("GetDispatchNode unclaimed: err: %v", err)
	}
	if owner.Kind != "unclaimed" {
		t.Fatalf("GetDispatchNode unclaimed: kind=%q want %q", owner.Kind, "unclaimed")
	}
	if gotNode != fix.NodeID {
		t.Fatalf("GetDispatchNode unclaimed: nodeID=%v want %v", gotNode, fix.NodeID)
	}

	// ---- GetDispatchNode: claimed_by branch ----
	supID := "queue-in-tx-supervisor"
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		ok, err := q.ClaimDispatchRow(ctx, tx, dispatchID, supID)
		if err != nil {
			return err
		}
		if !ok {
			t.Errorf("ClaimDispatchRow returned !ok on unclaimed row")
		}
		return nil
	}); err != nil {
		t.Fatalf("claim tx: %v", err)
	}
	gotNode, owner, err = q.GetDispatchNode(ctx, dispatchID)
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

	// ---- RemoveForNodeInTx: rollback preserves the row ----
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := q.RemoveForNodeInTx(ctx, fix.NodeID, supID, tx); err != nil {
			return err
		}
		return rollbackErr
	}); !errors.Is(err, rollbackErr) {
		t.Fatalf("RemoveForNodeInTx rollback: expected rollbackErr, got %v", err)
	}
	_, owner, err = q.GetDispatchNode(ctx, dispatchID)
	if err != nil {
		t.Fatalf("GetDispatchNode after rollback: err: %v", err)
	}
	if owner.Kind != "claimed_by" || owner.SupervisorID != supID {
		t.Fatalf("RemoveForNodeInTx rollback failed: row gone or claim cleared (kind=%q, sup=%q)",
			owner.Kind, owner.SupervisorID)
	}

	// ---- RemoveForNodeInTx: claimant-guard mismatch is no-op ----
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return q.RemoveForNodeInTx(ctx, fix.NodeID, "different-supervisor", tx)
	}); err != nil {
		t.Fatalf("RemoveForNodeInTx wrong sup: %v", err)
	}
	_, owner, err = q.GetDispatchNode(ctx, dispatchID)
	if err != nil {
		t.Fatalf("GetDispatchNode after wrong-sup remove: err: %v", err)
	}
	if owner.Kind != "claimed_by" {
		t.Fatalf("RemoveForNodeInTx with wrong supervisor was not a no-op: kind=%q", owner.Kind)
	}

	// ---- RemoveForNodeInTx: commit deletes the row ----
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return q.RemoveForNodeInTx(ctx, fix.NodeID, supID, tx)
	}); err != nil {
		t.Fatalf("RemoveForNodeInTx commit: %v", err)
	}
	_, owner, err = q.GetDispatchNode(ctx, dispatchID)
	if err != nil {
		t.Fatalf("GetDispatchNode after delete: err: %v", err)
	}
	if owner.Kind != "not_found" {
		t.Fatalf("RemoveForNodeInTx commit did not delete row: kind=%q", owner.Kind)
	}
}

// selectCandidateIDForNode runs SelectCandidates inside an ephemeral tx
// and returns the dispatch id for the given node, or shared.UUID{} when
// no candidate row matches. The tx rolls back to release FOR UPDATE
// locks; this is read-only from the test's perspective.
func selectCandidateIDForNode(ctx context.Context, t *testing.T,
	store persistence.Store, q persistence.Queue, nodeID shared.UUID,
) shared.UUID {
	t.Helper()
	probeErr := errors.New("rollback probe")
	var found shared.UUID
	err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors: []string{"test-executor"},
			AcceptedStores:    []string{},
			Limit:             100,
		})
		if err != nil {
			return err
		}
		for _, c := range cands {
			if c.NodeID == nodeID {
				found = c.DispatchID
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
