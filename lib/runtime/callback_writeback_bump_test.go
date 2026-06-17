// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Unit coverage for the load-bearing property of TD-three-dispatch-deadlines:
// the §12.5 attributes writeback bumps col:rimsky_node_runs.last_progress_at
// in the SAME tx as the merge/upsert, so the quiet-period sweep cannot
// observe a partial state.

package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// writebackBumpTables is a persistence.Tables stub whose Transaction
// returns a synthetic Tx pointer that BOTH the NodeAttributes call and
// the BumpLastProgressAt call must receive — proving they are in the
// same tx. The embedded NodeAttributes implementation captures the tx
// it was called with; the embedded Queue does the same.
type writebackBumpTables struct {
	persistence.Tables
	tx    persistence.Tx
	attrs *writebackBumpAttrs
}

// txSentinel is a non-nil persistence.Tx sentinel the stub hands to the
// closure. Identity comparison against this value verifies both calls
// shared the same Transaction invocation.
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

// writebackBumpAttrs is a NodeAttributeTable stub that records the tx
// pointer it was called with so the test can compare it against the
// pointer Queue.BumpLastProgressAt received.
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

// writebackBumpQueue captures the tx + runID handed to BumpLastProgressAt.
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

// TestAttributesAdapter_MergeDelta_BumpsInSameTx exercises the
// load-bearing property: a §12.5 MergeDelta and the
// BumpLastProgressAt fire inside the SAME tx (same Tx pointer
// reaches both interfaces).
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

// TestAttributesAdapter_Upsert_BumpsInSameTx covers the same property
// for the Upsert path (some callers go through Upsert instead of
// MergeDelta).
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

// TestAttributesAdapter_MergeDelta_NoBumpWhenQueueAbsent covers the
// nil-Queue defensive path: the adapter still works in a configuration
// where last_progress_at tracking is disabled.
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
