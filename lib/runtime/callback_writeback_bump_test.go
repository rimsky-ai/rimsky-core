// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type writebackBumpTables struct {
	persistence.Tables
	tx    persistence.Tx
	attrs *writebackBumpAttrs
}

type txSentinel struct {
	persistence.TxMarker
}

func (w *writebackBumpTables) Transaction(ctx context.Context, fn func(ctx context.Context, tx persistence.Tx) error) error {
	w.tx = &txSentinel{}
	return fn(ctx, w.tx)
}

func (w *writebackBumpTables) NodeAttributes() persistence.NodeAttributeTable {
	return w.attrs
}

type writebackBumpAttrs struct {
	persistence.NodeAttributeTable
	mergedTx persistence.Tx
	mergedAt time.Time
}

func (a *writebackBumpAttrs) MergeDelta(_ context.Context, _ shared.UUID, _ map[string]any, tx persistence.Tx) error {
	a.mergedTx = tx
	a.mergedAt = time.Now()
	return nil
}

func (a *writebackBumpAttrs) Upsert(_ context.Context, _ shared.UUID, _ shared.UUID, _ map[string]any, tx persistence.Tx) error {
	a.mergedTx = tx
	a.mergedAt = time.Now()
	return nil
}

type writebackBumpQueue struct {
	persistence.Queue
	bumpedTx    persistence.Tx
	bumpedRunID shared.UUID
	bumpedAt    time.Time
	calls       int
}

func (q *writebackBumpQueue) BumpLastProgressAt(_ context.Context, tx persistence.Tx, runID shared.UUID, now time.Time) (bool, error) {
	q.bumpedTx = tx
	q.bumpedRunID = runID
	q.bumpedAt = now
	q.calls++
	return true, nil
}

func TestAttributesAdapter_MergeDelta_BumpsInSameTx(t *testing.T) {
	t.Parallel()
	attrs := &writebackBumpAttrs{}
	tables := &writebackBumpTables{attrs: attrs}
	queue := &writebackBumpQueue{}
	adapter := attributesStoreAdapter{store: tables, queue: queue}

	runID := shared.UUID(uuid.New())
	if err := adapter.MergeDelta(context.Background(), runID, map[string]any{"k": "v"}); err != nil {
		t.Fatalf("MergeDelta: %v", err)
	}
	if attrs.mergedTx == nil {
		t.Fatalf("MergeDelta closure did not invoke NodeAttributes.MergeDelta")
	}
	if queue.calls != 1 {
		t.Fatalf("BumpLastProgressAt calls = %d, want 1", queue.calls)
	}
	if queue.bumpedTx != attrs.mergedTx {
		t.Fatalf("tx pointer mismatch: merge=%p bump=%p — must share the same Transaction",
			attrs.mergedTx, queue.bumpedTx)
	}
	if queue.bumpedRunID != runID {
		t.Fatalf("BumpLastProgressAt runID = %s, want %s", queue.bumpedRunID, runID)
	}
}

func TestAttributesAdapter_Upsert_BumpsInSameTx(t *testing.T) {
	t.Parallel()
	attrs := &writebackBumpAttrs{}
	tables := &writebackBumpTables{attrs: attrs}
	queue := &writebackBumpQueue{}
	adapter := attributesStoreAdapter{store: tables, queue: queue}

	runID := shared.UUID(uuid.New())
	nodeID := shared.UUID(uuid.New())
	if err := adapter.Upsert(context.Background(), runID, nodeID, map[string]any{"k": "v"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if queue.calls != 1 {
		t.Fatalf("BumpLastProgressAt calls = %d, want 1", queue.calls)
	}
	if queue.bumpedTx != attrs.mergedTx {
		t.Fatalf("tx pointer mismatch: upsert=%p bump=%p — must share the same Transaction",
			attrs.mergedTx, queue.bumpedTx)
	}
}

func TestAttributesAdapter_MergeDelta_NoBumpWhenQueueAbsent(t *testing.T) {
	t.Parallel()
	attrs := &writebackBumpAttrs{}
	tables := &writebackBumpTables{attrs: attrs}
	adapter := attributesStoreAdapter{store: tables, queue: nil}

	runID := shared.UUID(uuid.New())
	if err := adapter.MergeDelta(context.Background(), runID, map[string]any{"k": "v"}); err != nil {
		t.Fatalf("MergeDelta: %v", err)
	}
	if attrs.mergedTx == nil {
		t.Fatalf("MergeDelta closure did not invoke NodeAttributes.MergeDelta")
	}
}
