// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package conformance

import (
	"context"
	"testing"

	"github.com/google/uuid"

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

	frameOp(ctx, t, d, "MarkRunningFrameTerminal (initial)", func(tx persistence.Tx) error {
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
		if row == nil || row.State != persistence.FrameStateRunning || row.StartedAt == nil {
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

	frameOp(ctx, t, d, "MarkRunningFrameTerminal failed", func(tx persistence.Tx) error {
		transitioned, err := frames.MarkRunningFrameTerminal(ctx, f2, persistence.FrameStateFailed, tx)
		if err != nil {
			return err
		}
		if !transitioned {
			t.Fatalf("failed-state MarkRunningFrameTerminal did not transition")
		}
		row, err := frames.GetForObservability(ctx, f2, tx)
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
