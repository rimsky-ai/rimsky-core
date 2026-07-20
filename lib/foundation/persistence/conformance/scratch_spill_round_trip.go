// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: blob-backend
package conformance

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func writeScratchThroughSpillDecision(
	ctx context.Context, t *testing.T, d persistence.Database,
	bb persistence.BlobBackend, threshold int, runID, nodeID shared.UUID, payload []byte,
) {
	t.Helper()
	var inline []byte
	var handle, handleBackend string
	if persistence.ShouldSpillBlob(bb, threshold, len(payload)) {
		h, err := bb.Write(ctx, persistence.BlobKey{NodeID: nodeID.String(), Hint: "scratch"}, payload)
		if err != nil {
			t.Fatalf("blob.Write: %v", err)
		}
		handle = string(h)
		handleBackend = bb.Name()
	} else {
		inline = payload
	}
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return d.Queue().WriteScratchInTx(ctx, tx, runID, inline, handle, handleBackend)
	}); err != nil {
		t.Fatalf("WriteScratchInTx: %v", err)
	}
}

// @concept: blob-backend
func testScratchOverThresholdSpillsThroughRealBackend(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	runID := seedClaimedRunForNode(ctx, t, d, fix, fix.NodeID, "scratch-spill-supervisor")

	mem := persistence.NewMemoryBackend()
	d.SetBlobBackend(mem, 8, time.Hour)

	payload := []byte(strings.Repeat("scratch-payload-well-over-the-spill-threshold-", 4))
	if len(payload) <= 8 {
		t.Fatalf("test fixture payload too small to exceed the spill threshold: %d bytes", len(payload))
	}

	writeScratchThroughSpillDecision(ctx, t, d, mem, 8, runID, fix.NodeID, payload)

	var gotInline []byte
	var gotHandle, gotHandleBackend string
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		gotInline, gotHandle, gotHandleBackend, err = d.Queue().LoadScratchInTx(ctx, tx, runID)
		return err
	}); err != nil {
		t.Fatalf("LoadScratchInTx: %v", err)
	}

	if len(gotInline) != 0 {
		t.Fatalf("scratch_inline = %q, want empty for a spilled over-threshold payload", gotInline)
	}
	if gotHandle == "" {
		t.Fatalf("scratch_handle is empty, want a blob handle for a spilled over-threshold payload")
	}
	if gotHandleBackend != mem.Name() {
		t.Fatalf("scratch_handle_backend = %q, want %q", gotHandleBackend, mem.Name())
	}

	roundTripped, err := mem.Read(ctx, persistence.Handle(gotHandle))
	if err != nil {
		t.Fatalf("blob.Read(%q): %v", gotHandle, err)
	}
	if string(roundTripped) != string(payload) {
		t.Fatalf("round-tripped scratch blob = %q, want %q", roundTripped, payload)
	}
}

// @concept: blob-backend
func testScratchAtOrBelowThresholdStaysInlineThroughRealBackend(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	runID := seedClaimedRunForNode(ctx, t, d, fix, fix.NodeID, "scratch-inline-supervisor")

	mem := persistence.NewMemoryBackend()
	d.SetBlobBackend(mem, 4096, time.Hour)

	payload := []byte("small scratch payload")

	writeScratchThroughSpillDecision(ctx, t, d, mem, 4096, runID, fix.NodeID, payload)

	var gotInline []byte
	var gotHandle, gotHandleBackend string
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		gotInline, gotHandle, gotHandleBackend, err = d.Queue().LoadScratchInTx(ctx, tx, runID)
		return err
	}); err != nil {
		t.Fatalf("LoadScratchInTx: %v", err)
	}

	if string(gotInline) != string(payload) {
		t.Fatalf("scratch_inline = %q, want %q", gotInline, payload)
	}
	if gotHandle != "" || gotHandleBackend != "" {
		t.Fatalf("expected empty scratch_handle/backend below spill threshold, got handle=%q backend=%q",
			gotHandle, gotHandleBackend)
	}
}
