// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package conformance

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
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

	frameOp(ctx, t, d, "MarkFrameEnded (initial)", func(tx persistence.Tx) error {
		transitioned, err := frames.MarkFrameEnded(ctx, fix.FrameID, tx)
		if err != nil {
			return err
		}
		if !transitioned {
			t.Fatalf("MarkFrameEnded did not transition the running frame")
		}
		transitioned, err = frames.MarkFrameEnded(ctx, fix.FrameID, tx)
		if err != nil {
			return err
		}
		if transitioned {
			t.Fatalf("second MarkFrameEnded reported transitioned=true on an ended frame")
		}
		return nil
	})
	frameOp(ctx, t, d, "GetForObservability terminal", func(tx persistence.Tx) error {
		row, err := frames.GetForObservability(ctx, fix.FrameID, tx)
		if err != nil {
			return err
		}
		if row == nil || row.State != "completed" || row.EndedAt == nil {
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

	scope2 := shared.UUID(uuid.New())
	var f2 shared.UUID
	frameOp(ctx, t, d, "InsertRunningFrame f2 (after terminal)", func(tx persistence.Tx) error {
		if err := d.Tables().RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:         scope2,
			GraphName:  spec.MainGraphName,
			InstanceID: fix.InstanceID,
		}); err != nil {
			return err
		}
		var err error
		f2, err = frames.InsertRunningFrame(ctx, fix.InstanceID, fix.MessageID, scope2, 600000, tx)
		return err
	})
	if f2 == fix.FrameID {
		t.Fatalf("InsertRunningFrame returned the same id as the terminal frame: %s", f2)
	}
	frameOp(ctx, t, d, "post-insert reads", func(tx persistence.Tx) error {
		row, err := frames.GetForObservability(ctx, f2, tx)
		if err != nil {
			return err
		}
		if row == nil || row.State != "running" || row.StartedAt == nil {
			t.Fatalf("running frame row = %+v, want state=running with started_at", row)
		}
		id, err := frames.GetRunningFrameID(ctx, fix.InstanceID, tx)
		if err != nil {
			return err
		}
		if id == nil || *id != f2 {
			t.Fatalf("GetRunningFrameID = %v, want %s", id, f2)
		}
		return nil
	})

	scope3 := shared.UUID(uuid.New())
	if err := inTx(ctx, d.Tables(), func(tx persistence.Tx) error {
		if err := d.Tables().RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:         scope3,
			GraphName:  spec.MainGraphName,
			InstanceID: fix.InstanceID,
		}); err != nil {
			return err
		}
		_, err := frames.InsertRunningFrame(ctx, fix.InstanceID, fix.MessageID, scope3, 600000, tx)
		return err
	}); err == nil {
		t.Fatalf("InsertRunningFrame while another running frame exists must fail; got nil")
	}

	failedRunID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, f2)
	if err := inTx(ctx, d.Tables(), func(tx persistence.Tx) error {
		return d.Tables().NodeRunTree().UpdateStateAndOutcome(ctx, tx, failedRunID, cascade.NodeStateFailed, nil)
	}); err != nil {
		t.Fatalf("seed failed node_run for f2: %v", err)
	}
	frameOp(ctx, t, d, "MarkFrameEnded (failed derivation)", func(tx persistence.Tx) error {
		transitioned, err := frames.MarkFrameEnded(ctx, f2, tx)
		if err != nil {
			return err
		}
		if !transitioned {
			t.Fatalf("MarkFrameEnded did not transition f2")
		}
		row, err := frames.GetForObservability(ctx, f2, tx)
		if err != nil {
			return err
		}
		if row == nil || row.State != "failed" || row.EndedAt == nil {
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

// @decision: frame-isolation-is-structural
func testFrameRowCascadeImmutable(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	frames := d.Tables().Frames()

	var before *persistence.FrameRow
	frameOp(ctx, t, d, "snapshot running frame", func(tx persistence.Tx) error {
		row, err := frames.GetForObservability(ctx, fix.FrameID, tx)
		before = row
		return err
	})
	if before == nil || before.State != "running" || before.EndedAt != nil {
		t.Fatalf("pre-cascade frame row = %+v, want state=running with ended_at unset", before)
	}

	frameOp(ctx, t, d, "cascade write set: MarkSourceNodeStale", func(tx persistence.Tx) error {
		_, err := frames.MarkSourceNodeStale(ctx, fix.InstanceID, fix.NodeID, fix.FrameID, tx)
		return err
	})
	runID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)
	frameOp(ctx, t, d, "cascade write set: run state transitions", func(tx persistence.Tx) error {
		if err := d.Tables().NodeRunTree().UpdateStateAndOutcome(ctx, tx, runID, cascade.NodeStateRunning, nil); err != nil {
			return err
		}
		return d.Tables().NodeRunTree().UpdateStateAndOutcome(ctx, tx, runID, cascade.NodeStateFresh, nil)
	})

	var after *persistence.FrameRow
	frameOp(ctx, t, d, "re-read frame row after cascade writes", func(tx persistence.Tx) error {
		row, err := frames.GetForObservability(ctx, fix.FrameID, tx)
		after = row
		return err
	})
	if after == nil {
		t.Fatalf("frame row vanished after cascade writes")
	}
	if after.FrameID != before.FrameID ||
		after.InstanceID != before.InstanceID ||
		after.TriggeringMessageID != before.TriggeringMessageID ||
		after.RootRunScopeID != before.RootRunScopeID ||
		after.FrameTimeoutMs != before.FrameTimeoutMs {
		t.Fatalf("cascade writes mutated frame identity columns:\nbefore=%+v\nafter=%+v", before, after)
	}
	if before.StartedAt == nil || after.StartedAt == nil || !after.StartedAt.Equal(*before.StartedAt) {
		t.Fatalf("cascade writes mutated started_at: before=%v after=%v", before.StartedAt, after.StartedAt)
	}
	if after.EndedAt != nil || after.State != "running" {
		t.Fatalf("cascade writes ended the frame: row=%+v — only the frame-engine reaper may stamp ended_at", after)
	}
}
