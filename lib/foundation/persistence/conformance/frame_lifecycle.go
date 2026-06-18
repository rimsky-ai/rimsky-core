// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func frameOp(ctx context.Context, t *testing.T, d persistence.Database, op string, fn func(tx persistence.Tx) error) {
	t.Helper()
	if err := inTx(ctx, d.Tables(), fn); err != nil {
		t.Fatalf("%s: %v", op, err)
	}
}

func testFrameLifecycleSerialQueue(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	frames := d.Tables().Frames()

	var f1, f2 shared.UUID
	frameOp(ctx, t, d, "InsertFrame f1", func(tx persistence.Tx) error {
		var err error
		f1, err = frames.InsertFrame(ctx, fix.InstanceID, fix.MessageID, 600000, tx)
		return err
	})
	time.Sleep(20 * time.Millisecond)
	frameOp(ctx, t, d, "InsertFrame f2", func(tx persistence.Tx) error {
		var err error
		f2, err = frames.InsertFrame(ctx, fix.InstanceID, fix.MessageID, 600000, tx)
		return err
	})
	if f1 == f2 {
		t.Fatalf("InsertFrame returned same id twice: f1 == f2 == %s", f1)
	}

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
		if mine[0].TriggeringMessageID != fix.MessageID {
			t.Fatalf("ready frame triggering message = %v, want %s", mine[0].TriggeringMessageID, fix.MessageID)
		}
		return nil
	})

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

	frameOp(ctx, t, d, "LookupFrameTimeoutMs", func(tx persistence.Tx) error {
		timeoutMs, err := frames.LookupFrameTimeoutMs(ctx, fix.InstanceID, tx)
		if err != nil {
			return err
		}
		if timeoutMs != 600000 {
			t.Fatalf("LookupFrameTimeoutMs = %d, want 600000", timeoutMs)
		}
		return nil
	})
}
