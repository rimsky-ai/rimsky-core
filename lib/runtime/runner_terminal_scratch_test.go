// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

type scratchRecorder struct {
	persistence.Queue
	writes int
	last   struct {
		nodeRunID     shared.UUID
		inline        []byte
		handle        string
		handleBackend string
	}
}

func (r *scratchRecorder) WriteScratchInTx(_ context.Context, _ persistence.Tx, nodeRunID shared.UUID, inline []byte, handle, handleBackend string) error {
	r.writes++
	r.last.nodeRunID = nodeRunID
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
		NodeRunID: shared.UUID{1},
		NodeID:    shared.UUID{2},
	}

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

func TestApplyTerminalScratchInTx_SubgraphExitIsNoOp(t *testing.T) {
	t.Parallel()
	rec := &scratchRecorder{}
	args := RunArgs{Queue: rec}
	acq := &acquisition{
		NodeRunID: shared.UUID{1},
		NodeID:    shared.UUID{2},
		NodeDef:   &node.TemplateNodeDef{IsSubgraphExit: true},
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
	args := RunArgs{Queue: rec}
	acq := &acquisition{
		NodeRunID: shared.UUID{1},
		NodeID:    shared.UUID{2},
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
	if rec.last.nodeRunID != acq.NodeRunID {
		t.Fatalf("dispatch id mismatch: got %v want %v", rec.last.nodeRunID, acq.NodeRunID)
	}
}
