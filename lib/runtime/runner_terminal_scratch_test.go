// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

type scratchRecorder struct {
	persistence.Queue
	writes int
	bumps  int
	last   struct {
		nodeRunID shared.UUID
		scratch   []byte
	}
}

func (r *scratchRecorder) WriteScratch(_ context.Context, nodeRunID shared.UUID, scratch []byte, _ persistence.Tx) error {
	r.writes++
	r.last.nodeRunID = nodeRunID
	r.last.scratch = append([]byte(nil), scratch...)
	return nil
}

func (r *scratchRecorder) BumpLastProgressAt(_ context.Context, _ shared.UUID, _ time.Time, _ persistence.Tx) (bool, error) {
	r.bumps++
	return true, nil
}

func TestApplyTerminalScratchInTx_EmptyScratchIsNoOp(t *testing.T) {
	t.Parallel()
	rec := &scratchRecorder{}
	args := RunArgs{Queue: rec}
	acq := &acquisition{
		NodeRunID: shared.UUID{1},
		NodeID:    shared.UUID{2},
	}
	if err := applyTerminalScratchInTx(context.Background(), args, acq, nil, nil); err != nil {
		t.Fatalf("applyTerminalScratchInTx nil scratch: %v", err)
	}
	if rec.writes != 0 {
		t.Fatalf("expected 0 writes for nil scratch, got %d", rec.writes)
	}

	if err := applyTerminalScratchInTx(context.Background(), args, acq, []byte{}, nil); err != nil {
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
	if err := applyTerminalScratchInTx(context.Background(), args, acq, []byte("ignored"), nil); err != nil {
		t.Fatalf("applyTerminalScratchInTx exit: %v", err)
	}
	if rec.writes != 0 {
		t.Fatalf("expected 0 writes for sub-graph exit, got %d", rec.writes)
	}
}

// @decision: scratch-column
// @decision: attribute-bytes-in-the-row
func TestApplyTerminalScratchInTx_WritesTheWholeValueToTheRow(t *testing.T) {
	t.Parallel()
	rec := &scratchRecorder{}
	args := RunArgs{Queue: rec}
	acq := &acquisition{
		NodeRunID: shared.UUID{1},
		NodeID:    shared.UUID{2},
	}
	payload := make([]byte, 512*1024)
	for i := range payload {
		payload[i] = byte('a' + i%26)
	}
	if err := applyTerminalScratchInTx(context.Background(), args, acq, payload, nil); err != nil {
		t.Fatalf("applyTerminalScratchInTx: %v", err)
	}
	if rec.writes != 1 {
		t.Fatalf("expected 1 write, got %d", rec.writes)
	}
	if rec.bumps != 1 {
		t.Fatalf("expected the scratch write to bump last_progress_at in the same tx, got %d bumps", rec.bumps)
	}
	if string(rec.last.scratch) != string(payload) {
		t.Fatalf("scratch bytes mismatch: got %d bytes want %d", len(rec.last.scratch), len(payload))
	}
	if rec.last.nodeRunID != acq.NodeRunID {
		t.Fatalf("dispatch id mismatch: got %v want %v", rec.last.nodeRunID, acq.NodeRunID)
	}
}
