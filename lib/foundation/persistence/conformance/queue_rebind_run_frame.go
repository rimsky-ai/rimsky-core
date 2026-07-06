// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := store.Frames().MarkFrameEnded(ctx, fix.FrameID, tx)
		return err
	}); err != nil {
		t.Fatalf("mark original frame completed: %v", err)
	}
	var newFrameID shared.UUID
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		newScope := seedMainRunScopeForInstance(ctx, t, tx, store, fix.InstanceID)
		fid, err := store.Frames().InsertRunningFrame(ctx, fix.InstanceID, fix.MessageID, newScope, 600000, tx)
		if err != nil {
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

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return q.RebindRunFrameInTx(ctx, tx, runID, newFrameID)
	}); err != nil {
		t.Fatalf("RebindRunFrameInTx idempotent re-bind: %v", err)
	}

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
