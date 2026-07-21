// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: frame

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

func failRunWithSignal(ctx context.Context, t *testing.T, d persistence.Database, runID shared.UUID, settlingSignal string) {
	t.Helper()
	frameOp(ctx, t, d, "fail run "+settlingSignal, func(tx persistence.Tx) error {
		sig := settlingSignal
		return d.Tables().Nodes().UpdateState(ctx, runID,
			cascade.NodeStateFailed, cascade.ReasonInstanceKilled, &sig, tx)
	})
}

func frameStateOf(ctx context.Context, t *testing.T, d persistence.Database, frameID shared.UUID) string {
	t.Helper()
	var state string
	frameOp(ctx, t, d, "GetForObservability", func(tx persistence.Tx) error {
		row, err := d.Tables().Frames().GetForObservability(ctx, frameID, tx)
		if err != nil {
			return err
		}
		if row == nil {
			t.Fatalf("frame %s not found", frameID)
		}
		state = row.State
		return nil
	})
	return state
}

func testFrameDerivedStateTerminatedOnKill(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	runID := seedConformanceRunForScope(ctx, t, d, fix.NodeID, fix.FrameID, fix.MainRunScopeID)

	failRunWithSignal(ctx, t, d, runID, cascade.SettlingSignalInstanceKilled)

	if got := frameStateOf(ctx, t, d, fix.FrameID); got != "running" {
		t.Fatalf("open killed frame state = %q, want running until ended_at stamped", got)
	}

	var ended int
	frameOp(ctx, t, d, "MarkOpenFramesEndedForInstance", func(tx persistence.Tx) error {
		n, err := d.Tables().Frames().MarkOpenFramesEndedForInstance(ctx, fix.InstanceID, tx)
		ended = n
		return err
	})
	if ended != 1 {
		t.Fatalf("MarkOpenFramesEndedForInstance ended %d frames, want 1", ended)
	}

	if got := frameStateOf(ctx, t, d, fix.FrameID); got != "terminated" {
		t.Fatalf("killed frame state = %q, want terminated (kill casualties must not surface as failed)", got)
	}

	frameOp(ctx, t, d, "MarkOpenFramesEndedForInstance idempotent", func(tx persistence.Tx) error {
		n, err := d.Tables().Frames().MarkOpenFramesEndedForInstance(ctx, fix.InstanceID, tx)
		if err != nil {
			return err
		}
		if n != 0 {
			t.Fatalf("second MarkOpenFramesEndedForInstance ended %d frames, want 0", n)
		}
		return nil
	})
}

func testFrameDerivedStateGenuineFailureStaysFailed(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	runID := seedConformanceRunForScope(ctx, t, d, fix.NodeID, fix.FrameID, fix.MainRunScopeID)

	frameOp(ctx, t, d, "fail run genuinely", func(tx persistence.Tx) error {
		sig := "terminal/error/some_genuine_failure"
		return d.Tables().Nodes().UpdateState(ctx, runID,
			cascade.NodeStateFailed, cascade.ReasonPolicyGiveUp, &sig, tx)
	})
	frameOp(ctx, t, d, "MarkFrameEnded", func(tx persistence.Tx) error {
		_, err := d.Tables().Frames().MarkFrameEnded(ctx, fix.FrameID, tx)
		return err
	})

	if got := frameStateOf(ctx, t, d, fix.FrameID); got != "failed" {
		t.Fatalf("genuinely failed frame state = %q, want failed", got)
	}
}

func testRunScopeListTreeDeepestFirst(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	parentRunID := seedConformanceRunForScope(ctx, t, d, fix.NodeID, fix.FrameID, fix.MainRunScopeID)

	root := fix.MainRunScopeID
	child := shared.UUID(uuid.New())
	grandchild := shared.UUID(uuid.New())
	closedChild := shared.UUID(uuid.New())
	frameOp(ctx, t, d, "seed scope tree", func(tx persistence.Tx) error {
		rootID := root
		childID := child
		if err := store.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID: child, ParentRunScopeID: &rootID, ParentNodeRunID: &parentRunID,
			GraphName: "sub", InstanceID: fix.InstanceID,
		}, tx); err != nil {
			return err
		}
		if err := store.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID: grandchild, ParentRunScopeID: &childID, ParentNodeRunID: &parentRunID,
			GraphName: spec.MainGraphName, PartitionKey: "partition-a", InstanceID: fix.InstanceID,
		}, tx); err != nil {
			return err
		}
		if err := store.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID: closedChild, ParentRunScopeID: &rootID, ParentNodeRunID: &parentRunID,
			GraphName: "sub-closed", InstanceID: fix.InstanceID,
		}, tx); err != nil {
			return err
		}
		return store.RunScopes().Close(ctx, closedChild, tx)
	})

	var tree []persistence.RunScopeRow
	frameOp(ctx, t, d, "ListTreeDeepestFirst", func(tx persistence.Tx) error {
		rows, err := store.RunScopes().ListTreeDeepestFirst(ctx, root, tx)
		tree = rows
		return err
	})
	if len(tree) != 4 {
		t.Fatalf("scopes in tree = %d, want 4 (closed child included); rows=%+v", len(tree), tree)
	}
	if tree[0].ID != grandchild {
		t.Fatalf("first scope = %s, want deepest (grandchild %s)", tree[0].ID, grandchild)
	}
	if tree[len(tree)-1].ID != root {
		t.Fatalf("last scope = %s, want root %s", tree[len(tree)-1].ID, root)
	}
	depth := map[shared.UUID]int{root: 0, child: 1, closedChild: 1, grandchild: 2}
	for i := 1; i < len(tree); i++ {
		if depth[tree[i-1].ID] < depth[tree[i].ID] {
			t.Fatalf("children must list before parents: %s (depth %d) listed after %s (depth %d)",
				tree[i].ID, depth[tree[i].ID], tree[i-1].ID, depth[tree[i-1].ID])
		}
	}
	var closedIncluded bool
	for _, r := range tree {
		if r.ID == closedChild {
			closedIncluded = true
			if r.ClosedAt == nil {
				t.Fatalf("closed child must carry its closed_at in the tree listing")
			}
		}
	}
	if !closedIncluded {
		t.Fatalf("closed scopes must be included so teardown can retry peer fan-out for them")
	}
}
