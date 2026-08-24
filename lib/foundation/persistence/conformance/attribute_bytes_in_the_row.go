// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package conformance

import (
	"context"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

const largeAttributeValueBytes = 512 * 1024

// @decision: attribute-bytes-in-the-row
func testLargeAttributeBagAndScratchRoundTripFromTheRow(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	q := d.Queue()

	runID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)

	payload := strings.Repeat("a", largeAttributeValueBytes)
	bag := map[string]any{"payload": payload}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if err := store.NodeAttributes().Upsert(ctx, runID, fix.NodeID, bag, tx); err != nil {
			return err
		}
		return store.NodeAttributes().SetDispatchInputBag(ctx, runID, fix.NodeID, bag, tx)
	}); err != nil {
		t.Fatalf("write a bag far past the retired spill threshold: %v", err)
	}

	var (
		row      *persistence.NodeAttributesRow
		inputBag map[string]any
	)
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.NodeAttributes().GetByRun(ctx, runID, tx)
		if err != nil {
			return err
		}
		row = r
		inputBag, err = store.NodeAttributes().GetDispatchInputBag(ctx, runID, tx)
		return err
	}); err != nil {
		t.Fatalf("read the bag back: %v", err)
	}
	if row == nil {
		t.Fatalf("GetByRun: row missing after a large Upsert")
	}
	if got, _ := row.Data["payload"].(string); got != payload {
		t.Fatalf("attribute bag round-trip lost bytes: got %d, want %d", len(got), len(payload))
	}
	if got, _ := inputBag["payload"].(string); got != payload {
		t.Fatalf("dispatch input bag round-trip lost bytes: got %d, want %d", len(got), len(payload))
	}

	scratch := []byte(payload)
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return q.WriteScratch(ctx, runID, scratch, tx)
	}); err != nil {
		t.Fatalf("write scratch far past the retired spill threshold: %v", err)
	}
	var loaded []byte
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		var lerr error
		loaded, lerr = q.LoadScratch(ctx, runID, tx)
		return lerr
	}); err != nil {
		t.Fatalf("read scratch back: %v", err)
	}
	if string(loaded) != string(scratch) {
		t.Fatalf("scratch round-trip lost bytes: got %d, want %d", len(loaded), len(scratch))
	}
}
