// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @constraint: Queue.RebindRunFrameInTx conformance area. Both
// drivers must agree on:
//
//   - Happy path: existing run row's frame_id is updated.
//   - Idempotency: re-binding to the same frame is a no-op.
//   - Missing row: returns persistence.ErrRunRowMissing rather than
//     silently no-op'ing.
package conformance

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func testQueueRebindRunFrameInTx(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	q := d.Queue()
	if q == nil {
		t.Fatalf("driver.Queue() returned nil")
	}

	runID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)

	// @constraint: Rebinding requires a second frame so the FK on
	// rimsky_node_runs.frame_id resolves cleanly (SQLite enforces this
	// FK; postgres does too via the migrations). Mark the original
	// (running) frame completed first — the unique
	// `uq_rimsky_frames_running` constraint admits at most one running
	// frame per instance.
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := store.Frames().MarkRunningFrameTerminal(ctx, fix.FrameID,
			persistence.FrameStateCompleted, tx)
		return err
	}); err != nil {
		t.Fatalf("mark original frame completed: %v", err)
	}
	var newFrameID shared.UUID
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		fid, err := store.Frames().InsertFrame(ctx, fix.InstanceID, fix.MessageID, 600000, tx)
		if err != nil {
			return err
		}
		if _, err := store.Frames().PromoteQueuedFrameToRunning(ctx, fid, tx); err != nil {
			return err
		}
		newFrameID = fid
		return nil
	}); err != nil {
		t.Fatalf("seed second frame: %v", err)
	}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return q.RebindRunFrameInTx(ctx, tx, runID, newFrameID)
	}); err != nil {
		t.Fatalf("RebindRunFrameInTx happy path: %v", err)
	}

	// @constraint: Re-binding to the SAME (now-current) frame is a
	// successful no-op-shaped call, not a missing-row error —
	// RowsAffected is still 1 because the UPDATE matches the row.
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return q.RebindRunFrameInTx(ctx, tx, runID, newFrameID)
	}); err != nil {
		t.Fatalf("RebindRunFrameInTx idempotent re-bind: %v", err)
	}

	// @constraint: Passing a never-seeded run id MUST return
	// ErrRunRowMissing, not nil — a silent no-op would mask programmer
	// errors.
	bogusID := shared.UUID(uuid.New())
	missErr := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return q.RebindRunFrameInTx(ctx, tx, bogusID, newFrameID)
	})
	if missErr == nil {
		t.Fatalf("RebindRunFrameInTx on missing row: expected error, got nil")
	}
	if !errors.Is(missErr, persistence.ErrRunRowMissing) {
		t.Fatalf("RebindRunFrameInTx on missing row: expected ErrRunRowMissing, got %v", missErr)
	}
}
