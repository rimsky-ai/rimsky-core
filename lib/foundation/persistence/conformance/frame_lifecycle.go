// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @constraint: FrameLifecycle conformance area.
// Pins the rimsky_frames state machine the frame engine
// (graph/frame) drives through the FrameTable surface:
//
//   - queued → running → completed|failed, each transition firing
//     exactly once (MarkRunningFrameTerminal /
//     PromoteQueuedFrameToRunning return transitioned=false when a
//     racing replica got there first).
//   - ListQueuedFramesReadyToStart's serial-queue gate: at most one
//     queued frame per instance — the oldest — and ONLY while the
//     instance has no running frame.
//   - GetRunningFrameID resolves the single running frame.
//   - EnqueueCoalesceFrame appends a second source node to the
//     pending coalesce row (same frame id back) instead of minting a
//     new frame, and never coalesces into a queued serial frame.
//   - LookupFrameResolutionMode surfaces the template's
//     (frame_resolution, frame_timeout_ms) pair.
//
// The ready-to-start query is a window/aggregate join with
// driver-specific idioms on each side — high drift risk, hence the
// identical observable assertions on both drivers.
package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// frameOp wraps a single FrameTable call in its own tx.
func frameOp(ctx context.Context, t *testing.T, d persistence.Database, op string, fn func(tx persistence.Tx) error) {
	t.Helper()
	if err := inTx(ctx, d.Tables(), fn); err != nil {
		t.Fatalf("%s: %v", op, err)
	}
}

// testFrameLifecycleSerialQueue covers the queued → running → terminal
// chain plus the one-queued-per-instance ready-to-start gate.
func testFrameLifecycleSerialQueue(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	frames := d.Tables().Frames()

	// @constraint: sqlite's stored queued_at precision is millisecond-grained;
	// the sleep guarantees f1 has a strictly-older queued_at than f2 so the
	// oldest-queued tie-break is observable across both drivers.
	var f1, f2 shared.UUID
	frameOp(ctx, t, d, "EnqueueSerialFrame f1", func(tx persistence.Tx) error {
		var err error
		f1, err = frames.EnqueueSerialFrame(ctx, fix.InstanceID, fix.NodeID, 600000, tx)
		return err
	})
	time.Sleep(20 * time.Millisecond)
	frameOp(ctx, t, d, "EnqueueSerialFrame f2", func(tx persistence.Tx) error {
		var err error
		f2, err = frames.EnqueueSerialFrame(ctx, fix.InstanceID, fix.NodeID, 600000, tx)
		return err
	})
	if f1 == f2 {
		t.Fatalf("serial enqueues coalesced: f1 == f2 == %s", f1)
	}

	// @constraint: serial-queue gate — while a running frame exists,
	// ListQueuedFramesReadyToStart MUST NOT surface any queued frame for
	// the instance.
	frameOp(ctx, t, d, "ListQueuedFramesReadyToStart (running gate)", func(tx persistence.Tx) error {
		ready, err := frames.ListQueuedFramesReadyToStart(ctx, tx)
		if err != nil {
			return err
		}
		for _, r := range ready {
			if r.InstanceID == fix.InstanceID {
				t.Fatalf("queued frame %s surfaced ready while frame %s is running", r.FrameID, fix.FrameID)
			}
		}
		return nil
	})

	// @constraint: MarkRunningFrameTerminal returns transitioned=true exactly
	// once per frame; a second call on a terminal frame returns
	// transitioned=false (race-safe at-most-once semantics).
	frameOp(ctx, t, d, "MarkRunningFrameTerminal", func(tx persistence.Tx) error {
		transitioned, err := frames.MarkRunningFrameTerminal(ctx, fix.FrameID, persistence.FrameStateCompleted, tx)
		if err != nil {
			return err
		}
		if !transitioned {
			t.Fatalf("MarkRunningFrameTerminal did not transition the running frame")
		}
		transitioned, err = frames.MarkRunningFrameTerminal(ctx, fix.FrameID, persistence.FrameStateCompleted, tx)
		if err != nil {
			return err
		}
		if transitioned {
			t.Fatalf("second MarkRunningFrameTerminal reported transitioned=true on a terminal frame")
		}
		return nil
	})
	frameOp(ctx, t, d, "GetForObservability terminal", func(tx persistence.Tx) error {
		row, err := frames.GetForObservability(ctx, fix.FrameID, tx)
		if err != nil {
			return err
		}
		if row == nil || row.State != persistence.FrameStateCompleted || row.EndedAt == nil {
			t.Fatalf("terminal frame row = %+v, want state=completed with ended_at", row)
		}
		return nil
	})
	frameOp(ctx, t, d, "GetRunningFrameID (none)", func(tx persistence.Tx) error {
		id, err := frames.GetRunningFrameID(ctx, fix.InstanceID, tx)
		if err != nil {
			return err
		}
		if id != nil {
			t.Fatalf("GetRunningFrameID = %s after terminal, want nil", *id)
		}
		return nil
	})

	// @constraint: with no running frame for the instance,
	// ListQueuedFramesReadyToStart surfaces exactly one queued frame — the
	// oldest by queued_at (f1) — carrying its source node.
	frameOp(ctx, t, d, "ListQueuedFramesReadyToStart (oldest)", func(tx persistence.Tx) error {
		ready, err := frames.ListQueuedFramesReadyToStart(ctx, tx)
		if err != nil {
			return err
		}
		var mine []persistence.FrameQueuedReady
		for _, r := range ready {
			if r.InstanceID == fix.InstanceID {
				mine = append(mine, r)
			}
		}
		if len(mine) != 1 || mine[0].FrameID != f1 {
			t.Fatalf("ready set = %+v, want exactly the oldest queued frame %s", mine, f1)
		}
		if len(mine[0].SourceNodeIDs) != 1 || mine[0].SourceNodeIDs[0] != fix.NodeID {
			t.Fatalf("ready frame source nodes = %v, want [%s]", mine[0].SourceNodeIDs, fix.NodeID)
		}
		return nil
	})

	// @constraint: PromoteQueuedFrameToRunning returns transitioned=true
	// exactly once, stamps started_at, and re-closes the ready-to-start gate
	// (f2 stays queued behind the new running f1).
	frameOp(ctx, t, d, "PromoteQueuedFrameToRunning", func(tx persistence.Tx) error {
		transitioned, err := frames.PromoteQueuedFrameToRunning(ctx, f1, tx)
		if err != nil {
			return err
		}
		if !transitioned {
			t.Fatalf("PromoteQueuedFrameToRunning did not promote the queued frame")
		}
		transitioned, err = frames.PromoteQueuedFrameToRunning(ctx, f1, tx)
		if err != nil {
			return err
		}
		if transitioned {
			t.Fatalf("second PromoteQueuedFrameToRunning reported transitioned=true")
		}
		return nil
	})
	frameOp(ctx, t, d, "post-promote reads", func(tx persistence.Tx) error {
		row, err := frames.GetForObservability(ctx, f1, tx)
		if err != nil {
			return err
		}
		if row == nil || row.State != persistence.FrameStateRunning || row.StartedAt == nil {
			t.Fatalf("promoted frame row = %+v, want state=running with started_at", row)
		}
		id, err := frames.GetRunningFrameID(ctx, fix.InstanceID, tx)
		if err != nil {
			return err
		}
		if id == nil || *id != f1 {
			t.Fatalf("GetRunningFrameID = %v, want %s", id, f1)
		}
		ready, err := frames.ListQueuedFramesReadyToStart(ctx, tx)
		if err != nil {
			return err
		}
		for _, r := range ready {
			if r.InstanceID == fix.InstanceID {
				t.Fatalf("queued frame %s surfaced ready while %s is running", r.FrameID, f1)
			}
		}
		return nil
	})

	// @constraint: the failed terminal state lands via the same
	// MarkRunningFrameTerminal path as completed, stamping ended_at
	// identically.
	frameOp(ctx, t, d, "MarkRunningFrameTerminal failed", func(tx persistence.Tx) error {
		transitioned, err := frames.MarkRunningFrameTerminal(ctx, f1, persistence.FrameStateFailed, tx)
		if err != nil {
			return err
		}
		if !transitioned {
			t.Fatalf("failed-state MarkRunningFrameTerminal did not transition")
		}
		row, err := frames.GetForObservability(ctx, f1, tx)
		if err != nil {
			return err
		}
		if row == nil || row.State != persistence.FrameStateFailed || row.EndedAt == nil {
			t.Fatalf("failed frame row = %+v, want state=failed with ended_at", row)
		}
		return nil
	})

	// @constraint: LookupFrameResolutionMode surfaces the template's
	// (frame_resolution, frame_timeout_ms) pair exactly as configured —
	// the producer path depends on this read.
	frameOp(ctx, t, d, "LookupFrameResolutionMode", func(tx persistence.Tx) error {
		mode, timeoutMs, err := frames.LookupFrameResolutionMode(ctx, fix.InstanceID, tx)
		if err != nil {
			return err
		}
		if mode != persistence.FrameResolutionModeSerialQueue || timeoutMs != 600000 {
			t.Fatalf("LookupFrameResolutionMode = (%s, %d), want (serial_queue, 600000)", mode, timeoutMs)
		}
		return nil
	})
}

// testFrameLifecycleCoalesce covers EnqueueCoalesceFrame's
// append-to-pending behavior: two coalesce enqueues land on ONE queued
// frame carrying both source nodes, and a queued SERIAL frame never
// absorbs a coalesce source.
func testFrameLifecycleCoalesce(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	frames := d.Tables().Frames()

	nodeB := seedExtraNode(ctx, t, d, fix, "coalesce-node-b")
	nodeC := seedExtraNode(ctx, t, d, fix, "coalesce-node-c")

	// @constraint: EnqueueCoalesceFrame MUST NOT fold into a queued SERIAL
	// frame — coalesce-append targets only the pending coalesce row.
	var serialF shared.UUID
	frameOp(ctx, t, d, "EnqueueSerialFrame", func(tx persistence.Tx) error {
		var err error
		serialF, err = frames.EnqueueSerialFrame(ctx, fix.InstanceID, fix.NodeID, 600000, tx)
		return err
	})

	var fc1, fc2 shared.UUID
	frameOp(ctx, t, d, "EnqueueCoalesceFrame first", func(tx persistence.Tx) error {
		var err error
		fc1, err = frames.EnqueueCoalesceFrame(ctx, fix.InstanceID, nodeB, 600000, tx)
		return err
	})
	if fc1 == serialF {
		t.Fatalf("coalesce enqueue folded into the queued SERIAL frame %s", serialF)
	}
	frameOp(ctx, t, d, "EnqueueCoalesceFrame second", func(tx persistence.Tx) error {
		var err error
		fc2, err = frames.EnqueueCoalesceFrame(ctx, fix.InstanceID, nodeC, 600000, tx)
		return err
	})
	if fc1 != fc2 {
		t.Fatalf("second coalesce enqueue minted a new frame %s, want append to %s", fc2, fc1)
	}

	// @constraint: once the coalesce frame surfaces ready, its SourceNodeIDs
	// MUST carry BOTH appended sources (nodeB and nodeC) — the second
	// EnqueueCoalesceFrame appended to the same row rather than minting a
	// new frame.
	frameOp(ctx, t, d, "drain to coalesce frame", func(tx persistence.Tx) error {
		if _, err := frames.MarkRunningFrameTerminal(ctx, fix.FrameID, persistence.FrameStateCompleted, tx); err != nil {
			return err
		}
		if _, err := frames.PromoteQueuedFrameToRunning(ctx, serialF, tx); err != nil {
			return err
		}
		if _, err := frames.MarkRunningFrameTerminal(ctx, serialF, persistence.FrameStateCompleted, tx); err != nil {
			return err
		}
		return nil
	})
	frameOp(ctx, t, d, "coalesce frame ready with both sources", func(tx persistence.Tx) error {
		ready, err := frames.ListQueuedFramesReadyToStart(ctx, tx)
		if err != nil {
			return err
		}
		var mine *persistence.FrameQueuedReady
		for i, r := range ready {
			if r.InstanceID == fix.InstanceID {
				if mine != nil {
					t.Fatalf("more than one ready frame for the instance: %+v", ready)
				}
				mine = &ready[i]
			}
		}
		if mine == nil || mine.FrameID != fc1 {
			t.Fatalf("ready frame = %+v, want coalesce frame %s", mine, fc1)
		}
		got := map[shared.UUID]bool{}
		for _, id := range mine.SourceNodeIDs {
			got[id] = true
		}
		if len(mine.SourceNodeIDs) != 2 || !got[nodeB] || !got[nodeC] {
			t.Fatalf("coalesce frame sources = %v, want exactly {%s, %s}", mine.SourceNodeIDs, nodeB, nodeC)
		}
		return nil
	})
}
