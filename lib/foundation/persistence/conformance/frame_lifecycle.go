// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package conformance

import (
	"context"
	"testing"
	"time"

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

	var firstEndedAt *time.Time
	frameOp(ctx, t, d, "MarkFrameEnded (initial)", func(tx persistence.Tx) error {
		transitioned, err := frames.MarkFrameEnded(ctx, fix.FrameID, tx)
		if err != nil {
			return err
		}
		if !transitioned {
			t.Fatalf("MarkFrameEnded did not transition the running frame")
		}
		row, err := frames.GetForObservability(ctx, fix.FrameID, tx)
		if err != nil {
			return err
		}
		if row == nil || row.EndedAt == nil {
			t.Fatalf("frame row after first MarkFrameEnded = %+v, want ended_at set", row)
		}
		firstEndedAt = row.EndedAt
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
	frameOp(ctx, t, d, "MarkFrameEnded (cross-transaction re-end)", func(tx persistence.Tx) error {
		transitioned, err := frames.MarkFrameEnded(ctx, fix.FrameID, tx)
		if err != nil {
			return err
		}
		if transitioned {
			t.Fatalf("MarkFrameEnded in a fresh transaction reported transitioned=true on an already-ended frame")
		}
		row, err := frames.GetForObservability(ctx, fix.FrameID, tx)
		if err != nil {
			return err
		}
		if row == nil || row.EndedAt == nil || firstEndedAt == nil || !row.EndedAt.Equal(*firstEndedAt) {
			t.Fatalf("MarkFrameEnded across a fresh transaction re-stamped ended_at: first=%v second=%v", firstEndedAt, row)
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
		if err := d.Tables().RunScopes().Create(ctx, persistence.RunScopeRow{
			ID:         scope2,
			GraphName:  spec.MainGraphName,
			InstanceID: fix.InstanceID,
		}, tx); err != nil {
			return err
		}
		var err error
		f2, err = frames.InsertRunningFrame(ctx, fix.InstanceID, fix.MessageID, scope2, tx)
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
		if row.RootRunScopeID != scope2 {
			t.Fatalf("InsertRunningFrame f2 root_run_scope_id = %s, want the supplied scope %s", row.RootRunScopeID, scope2)
		}
		if row.TriggeringMessageID != fix.MessageID {
			t.Fatalf("InsertRunningFrame f2 triggering_message_id = %s, want the supplied message %s", row.TriggeringMessageID, fix.MessageID)
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
		return d.Tables().RunScopes().Create(ctx, persistence.RunScopeRow{
			ID:         scope3,
			GraphName:  spec.MainGraphName,
			InstanceID: fix.InstanceID,
		}, tx)
	}); err != nil {
		t.Fatalf("seed scope3: %v", err)
	}
	if err := inTx(ctx, d.Tables(), func(tx persistence.Tx) error {
		_, err := frames.InsertRunningFrame(ctx, fix.InstanceID, fix.MessageID, scope3, tx)
		return err
	}); err == nil {
		t.Fatalf("InsertRunningFrame while another running frame exists must fail; got nil")
	}

	fkFix := seedFixtureSet(ctx, t, d)
	if err := inTx(ctx, d.Tables(), func(tx persistence.Tx) error {
		_, err := frames.MarkFrameEnded(ctx, fkFix.FrameID, tx)
		return err
	}); err != nil {
		t.Fatalf("end fkFix running frame: %v", err)
	}
	fkScope := shared.UUID(uuid.New())
	if err := inTx(ctx, d.Tables(), func(tx persistence.Tx) error {
		return d.Tables().RunScopes().Create(ctx, persistence.RunScopeRow{
			ID:         fkScope,
			GraphName:  spec.MainGraphName,
			InstanceID: fkFix.InstanceID,
		}, tx)
	}); err != nil {
		t.Fatalf("seed fkScope: %v", err)
	}
	if err := inTx(ctx, d.Tables(), func(tx persistence.Tx) error {
		_, err := frames.InsertRunningFrame(ctx, fkFix.InstanceID, shared.UUID(uuid.New()), fkScope, tx)
		return err
	}); err == nil {
		t.Fatalf("InsertRunningFrame with a nonexistent triggering message must fail (RESTRICT on rimsky_messages); got nil")
	}
	if err := inTx(ctx, d.Tables(), func(tx persistence.Tx) error {
		_, err := frames.InsertRunningFrame(ctx, fkFix.InstanceID, fkFix.MessageID, shared.UUID(uuid.New()), tx)
		return err
	}); err == nil {
		t.Fatalf("InsertRunningFrame with a nonexistent run scope must fail (REFERENCES rimsky_run_scopes); got nil")
	}

	failedRunID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, f2)
	if err := inTx(ctx, d.Tables(), func(tx persistence.Tx) error {
		return d.Tables().NodeRunTree().UpdateStateAndOutcome(ctx, failedRunID, cascade.NodeStateFailed, nil, false, tx)
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
}

func testFrameEndedHeartbeatDoesNotBumpLastProgressAt(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	frames := d.Tables().Frames()
	supID := "supervisor-ended-frame-heartbeat"

	nodeRunID := seedClaimedGuardRun(ctx, t, d, fix, supID)

	frameOp(ctx, t, d, "end frame before heartbeat", func(tx persistence.Tx) error {
		transitioned, err := frames.MarkFrameEnded(ctx, fix.FrameID, tx)
		if err != nil {
			return err
		}
		if !transitioned {
			t.Fatalf("MarkFrameEnded did not transition the running frame")
		}
		return nil
	})

	var before *persistence.FrameRow
	frameOp(ctx, t, d, "snapshot ended frame", func(tx persistence.Tx) error {
		row, err := frames.GetForObservability(ctx, fix.FrameID, tx)
		before = row
		return err
	})
	if before == nil || before.EndedAt == nil {
		t.Fatalf("ended frame row = %+v, want ended_at set", before)
	}

	sig := "terminal/error/test_failure"
	frameOp(ctx, t, d, "heartbeat run in ended frame", func(tx persistence.Tx) error {
		return d.Tables().Nodes().UpdateState(ctx, nodeRunID,
			cascade.NodeStateFailed, cascade.ReasonPolicyGiveUp, &sig, tx)
	})

	var after *persistence.FrameRow
	frameOp(ctx, t, d, "re-snapshot ended frame", func(tx persistence.Tx) error {
		row, err := frames.GetForObservability(ctx, fix.FrameID, tx)
		after = row
		return err
	})
	if after == nil {
		t.Fatalf("frame row vanished after heartbeat")
	}
	beforeStr, afterStr := "<nil>", "<nil>"
	if before.LastProgressAt != nil {
		beforeStr = before.LastProgressAt.String()
	}
	if after.LastProgressAt != nil {
		afterStr = after.LastProgressAt.String()
	}
	if beforeStr != afterStr {
		t.Fatalf("heartbeat on a run in an ended frame bumped last_progress_at: before=%s after=%s", beforeStr, afterStr)
	}
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

	runID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)
	frameOp(ctx, t, d, "cascade write set: run state transitions", func(tx persistence.Tx) error {
		if err := d.Tables().NodeRunTree().UpdateStateAndOutcome(ctx, runID, cascade.NodeStateRunning, nil, false, tx); err != nil {
			return err
		}
		return d.Tables().NodeRunTree().UpdateStateAndOutcome(ctx, runID, cascade.NodeStateFresh, nil, false, tx)
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
		after.RootRunScopeID != before.RootRunScopeID {
		t.Fatalf("cascade writes mutated frame identity columns:\nbefore=%+v\nafter=%+v", before, after)
	}
	if before.StartedAt == nil || after.StartedAt == nil || !after.StartedAt.Equal(*before.StartedAt) {
		t.Fatalf("cascade writes mutated started_at: before=%v after=%v", before.StartedAt, after.StartedAt)
	}
	if after.EndedAt != nil || after.State != "running" {
		t.Fatalf("cascade writes ended the frame: row=%+v — only the frame-engine reaper may stamp ended_at", after)
	}
}
