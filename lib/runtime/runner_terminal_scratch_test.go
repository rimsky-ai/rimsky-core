// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

type failingWriteBlobBackend struct{ persistence.BlobBackend }

func (failingWriteBlobBackend) Write(context.Context, persistence.BlobKey, []byte) (persistence.Handle, error) {
	return "", errors.New("simulated blob write failure")
}
func (failingWriteBlobBackend) Name() string { return "failing" }

type scratchRecorder struct {
	persistence.Queue
	writes int
	bumps  int
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

func (r *scratchRecorder) BumpLastProgressAt(_ context.Context, _ persistence.Tx, _ shared.UUID, _ time.Time) (bool, error) {
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
	if rec.bumps != 1 {
		t.Fatalf("expected the scratch write to bump last_progress_at in the same tx, got %d bumps", rec.bumps)
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

func TestApplyTerminalScratchInTx_OverThresholdSpillsToBlobBackend(t *testing.T) {
	t.Parallel()
	rec := &scratchRecorder{}
	blob := persistence.NewMemoryBackend()
	args := RunArgs{
		Queue:              rec,
		Blob:               blob,
		BlobSpillThreshold: 8,
	}
	acq := &acquisition{
		NodeRunID: shared.UUID{1},
		NodeID:    shared.UUID{2},
	}
	payload := []byte("this scratch payload is well over the spill threshold")

	if err := applyTerminalScratchInTx(context.Background(), args, nil, acq, payload); err != nil {
		t.Fatalf("applyTerminalScratchInTx: %v", err)
	}
	if rec.writes != 1 {
		t.Fatalf("expected 1 write, got %d", rec.writes)
	}
	if len(rec.last.inline) != 0 {
		t.Fatalf("expected empty inline for spilled scratch, got %q", rec.last.inline)
	}
	if rec.last.handle == "" {
		t.Fatalf("expected non-empty blob handle for spilled scratch")
	}
	if rec.last.handleBackend != blob.Name() {
		t.Fatalf("handle backend = %q; want %q", rec.last.handleBackend, blob.Name())
	}

	roundTripped, err := blob.Read(context.Background(), persistence.Handle(rec.last.handle))
	if err != nil {
		t.Fatalf("blob.Read(%q): %v", rec.last.handle, err)
	}
	if string(roundTripped) != string(payload) {
		t.Fatalf("round-tripped blob bytes = %q; want %q", roundTripped, payload)
	}
}

func TestApplyTerminalScratchInTx_AtOrBelowThresholdStaysInlineDespiteBlob(t *testing.T) {
	t.Parallel()
	rec := &scratchRecorder{}
	blob := persistence.NewMemoryBackend()
	args := RunArgs{
		Queue:              rec,
		Blob:               blob,
		BlobSpillThreshold: 64,
	}
	acq := &acquisition{
		NodeRunID: shared.UUID{1},
		NodeID:    shared.UUID{2},
	}
	payload := []byte("small scratch")

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
		t.Fatalf("expected empty handle/backend below spill threshold, got handle=%q backend=%q",
			rec.last.handle, rec.last.handleBackend)
	}
}

func TestApplyTerminalScratchInTx_BlobSpillFailureNeverLogsScratchContent(t *testing.T) {
	t.Parallel()
	rec := &scratchRecorder{}
	log := shared.NewCapturingLogger()
	args := RunArgs{
		Queue:              rec,
		Blob:               failingWriteBlobBackend{},
		BlobSpillThreshold: 4,
		Logger:             log,
	}
	acq := &acquisition{
		NodeRunID: shared.UUID{1},
		NodeID:    shared.UUID{2},
	}

	marker := "top-secret-scratch-payload-marker-9f3a"
	payload := []byte(strings.Repeat("x", 32) + marker)

	if err := applyTerminalScratchInTx(context.Background(), args, nil, acq, payload); err != nil {
		t.Fatalf("applyTerminalScratchInTx: %v", err)
	}
	if string(rec.last.inline) != string(payload) {
		t.Fatalf("expected write-failure fallback to inline payload, got %q", rec.last.inline)
	}

	for _, r := range log.Records() {
		if strings.Contains(r.Msg, marker) {
			t.Fatalf("log message leaked scratch content: %q", r.Msg)
		}
		for k, v := range r.Fields {
			s := fmt.Sprintf("%v", v)
			if strings.Contains(s, marker) {
				t.Fatalf("log field %q leaked scratch content: %q", k, s)
			}
		}
	}
}
