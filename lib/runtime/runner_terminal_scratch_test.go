// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Unit tests for applyTerminalScratchInTx. The empty-terminal-scratch
// contract is documented in
// `.ok-planner/design/decisions/scratch-protocol.md` — an empty
// terminal-attach is a no-op against the dispatch row's persisted
// scratch (preserves prior mid-dispatch / recovery-copied state); the
// executor's "clear" lane is the mid-dispatch scratch HTTP callback
// route with an empty body, not the terminal outcome's scratch field.

package runtime

import (
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

// scratchRecorder is a minimal Queue stub that records WriteScratchInTx
// invocations. Every other Queue method is a no-op / zero-value return
// — only the scratch surface is exercised here.
type scratchRecorder struct {
	persistence.Queue
	writes int
	last   struct {
		dispatchID    shared.UUID
		inline        []byte
		handle        string
		handleBackend string
	}
}

func (r *scratchRecorder) WriteScratchInTx(_ context.Context, _ persistence.Tx, dispatchID shared.UUID, inline []byte, handle, handleBackend string) error {
	r.writes++
	r.last.dispatchID = dispatchID
	r.last.inline = append([]byte(nil), inline...)
	r.last.handle = handle
	r.last.handleBackend = handleBackend
	return nil
}

func TestApplyTerminalScratchInTx_EmptyScratchIsNoOp(t *testing.T) {
	t.Parallel()
	rec := &scratchRecorder{}
	args := RunArgs{Queue: rec}
	acq := &acquisition{
		DispatchID: shared.UUID{1},
		NodeID:     shared.UUID{2},
	}

	// @constraint: empty scratch — WriteScratchInTx MUST NOT be issued.
	// Any prior row state (mid-dispatch callback write, recovery-copied
	// bytes) survives.
	if err := applyTerminalScratchInTx(context.Background(), args, nil, acq, nil); err != nil {
		t.Fatalf("applyTerminalScratchInTx nil scratch: %v", err)
	}
	if rec.writes != 0 {
		t.Fatalf("expected 0 writes for nil scratch, got %d", rec.writes)
	}

	if err := applyTerminalScratchInTx(context.Background(), args, nil, acq, []byte{}); err != nil {
		t.Fatalf("applyTerminalScratchInTx empty scratch: %v", err)
	}
	if rec.writes != 0 {
		t.Fatalf("expected 0 writes for empty scratch, got %d", rec.writes)
	}
}

// TestApplyTerminalScratchInTx_SubgraphExitIsNoOp pins the carve-out:
// every terminal site (Success, Error/retry-after-error, infra-reenqueue)
// must agree that a sub-graph exit's dispatch row stays empty. The
// gate lives inside applyTerminalScratchInTx (rather than at each
// call site) so the three sites cannot silently drift on this rule —
// if any future site reuses the helper it inherits the carve-out
// automatically.
func TestApplyTerminalScratchInTx_SubgraphExitIsNoOp(t *testing.T) {
	t.Parallel()
	rec := &scratchRecorder{}
	args := RunArgs{Queue: rec}
	acq := &acquisition{
		DispatchID: shared.UUID{1},
		NodeID:     shared.UUID{2},
		NodeDef:    &node.TemplateNodeDef{IsSubgraphExit: true},
	}
	if err := applyTerminalScratchInTx(context.Background(), args, nil, acq, []byte("ignored")); err != nil {
		t.Fatalf("applyTerminalScratchInTx exit: %v", err)
	}
	if rec.writes != 0 {
		t.Fatalf("expected 0 writes for sub-graph exit, got %d", rec.writes)
	}
}

func TestApplyTerminalScratchInTx_NonEmptyWritesInline(t *testing.T) {
	t.Parallel()
	rec := &scratchRecorder{}
	// @deliberate: zero BlobSpillThreshold + nil Blob means
	// shouldSpillBlob returns false, so this lands inline regardless
	// of size.
	args := RunArgs{Queue: rec}
	acq := &acquisition{
		DispatchID: shared.UUID{1},
		NodeID:     shared.UUID{2},
	}
	payload := []byte("scratch-bytes")
	if err := applyTerminalScratchInTx(context.Background(), args, nil, acq, payload); err != nil {
		t.Fatalf("applyTerminalScratchInTx: %v", err)
	}
	if rec.writes != 1 {
		t.Fatalf("expected 1 write, got %d", rec.writes)
	}
	if string(rec.last.inline) != string(payload) {
		t.Fatalf("inline bytes mismatch: got %q want %q", rec.last.inline, payload)
	}
	if rec.last.handle != "" || rec.last.handleBackend != "" {
		t.Fatalf("expected empty handle/backend for inline write, got handle=%q backend=%q",
			rec.last.handle, rec.last.handleBackend)
	}
	if rec.last.dispatchID != acq.DispatchID {
		t.Fatalf("dispatch id mismatch: got %v want %v", rec.last.dispatchID, acq.DispatchID)
	}
}
