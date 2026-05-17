// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// nodes_mark_stale_for_cascade.go — NodesMarkStaleForCascade conformance area.
//
// Covers NodeTable.MarkStaleForCascade — the cascade target used by the
// supervisor's terminal-complete path. Asserts the gated predicate:
//
//   - fresh                       -> stale + frame_id (matches)
//   - stale + frame_id IS NULL    -> stale + frame_id (matches)
//   - already running             -> no-op (predicate excludes running)
//
// Both drivers must produce the same behaviour on each row.
package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
)

func testNodesMarkStaleForCascade(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	// Seed three sibling nodes against the same instance: one fresh, one
	// stale-with-NULL-frame, one running. Then run MarkStaleForCascade
	// against each with a target frame_id and inspect the result.
	//
	// We need two different frames so we can pin the running node to one
	// and verify the cascade target (the other) does NOT overwrite it.
	cascadeFrameID := fix.FrameID

	// Enqueue a second serial-queue frame for the same instance so the
	// FK on rimsky_nodes.frame_id stays valid for the running-node pin.
	var otherFrameID shared.UUID
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		fid, err := store.Frames().EnqueueSerialFrame(ctx, fix.InstanceID, fix.NodeID, 600000, tx)
		if err != nil {
			return err
		}
		otherFrameID = fid
		return nil
	}); err != nil {
		t.Fatalf("seed otherFrame: %v", err)
	}

	freshID := uuid.New()
	staleNullFrameID := uuid.New()
	runningID := uuid.New()

	// Post-stage-3 cutover: state lives on rimsky_node_runs. Stale /
	// running rows are seeded by inserting an in-flight pending /
	// active run row via Queue.EnqueueInTx + Nodes().UpdateState.
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		// fresh node: created in 'fresh' by NodeTable.Create — no
		// in-flight run row.
		if _, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID:         freshID,
			InstanceID: fix.InstanceID,
			NodeType:   "cascade-fresh",
			Executor:   "test-executor",
		}, tx); err != nil {
			return err
		}

		// stale node with NULL node-row frame_id: created fresh, then
		// transitioned to stale by inserting a pending run row pinned
		// to the cascade frame. (Pre-cutover this row had a NULL
		// rimsky_nodes.frame_id; under the cutover the node.frame_id
		// is the cascade frame after MarkStaleForCascade. The "stale +
		// NULL frame" pre-cutover predicate has no direct post-cutover
		// equivalent — the closest analogue is "in-flight stale row
		// already exists; MarkStaleForCascade is a no-op". We test the
		// no-op-on-existing-in-flight-stale case below.)
		if _, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID:         staleNullFrameID,
			InstanceID: fix.InstanceID,
			NodeType:   "cascade-stale-null",
			Executor:   "test-executor",
		}, tx); err != nil {
			return err
		}
		// Seed an in-flight stale run row for staleNullFrameID pinned
		// to the cascade frame so the cascade target is a no-op.
		if err := d.Queue().EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:       staleNullFrameID,
			ExecutorName: "test-executor",
			EnqueuedAt:   time.Now().UTC(),
			FrameID:      cascadeFrameID,
		}, tx); err != nil {
			return err
		}
		// Pin the node row to the cascade frame too — pre-cutover the
		// "stale + NULL frame" row had MarkStaleForCascade re-bind it;
		// post-cutover this is the equivalent state.
		if err := store.Nodes().SetFrameID(ctx, staleNullFrameID, &cascadeFrameID, tx); err != nil {
			return err
		}

		// running node: pending run row → claimed → running. Pin its
		// node-row frame_id to otherFrameID so the assertion can check
		// that MarkStaleForCascade did not overwrite it.
		if _, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID:         runningID,
			InstanceID: fix.InstanceID,
			NodeType:   "cascade-running",
			Executor:   "test-executor",
		}, tx); err != nil {
			return err
		}
		if err := d.Queue().EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:       runningID,
			ExecutorName: "test-executor",
			EnqueuedAt:   time.Now().UTC(),
			FrameID:      otherFrameID,
		}, tx); err != nil {
			return err
		}
		if err := store.Nodes().UpdateState(ctx, runningID,
			cascade.NodeStateRunning, cascade.ReasonDispatchClaimed, "", tx); err != nil {
			return err
		}
		return store.Nodes().SetFrameID(ctx, runningID, &otherFrameID, tx)
	}); err != nil {
		t.Fatalf("seed nodes: %v", err)
	}

	// Issue MarkStaleForCascade against each.
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.Nodes().MarkStaleForCascade(ctx, freshID, cascadeFrameID, tx); err != nil {
			return err
		}
		if err := store.Nodes().MarkStaleForCascade(ctx, staleNullFrameID, cascadeFrameID, tx); err != nil {
			return err
		}
		return store.Nodes().MarkStaleForCascade(ctx, runningID, cascadeFrameID, tx)
	}); err != nil {
		t.Fatalf("MarkStaleForCascade: %v", err)
	}

	// fresh node: state=stale, frame_id=cascadeFrameID
	var gotFresh *persistence.NodeRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.Nodes().Get(ctx, freshID, tx)
		gotFresh = r
		return err
	}); err != nil {
		t.Fatalf("Get freshID: %v", err)
	}
	if gotFresh == nil {
		t.Fatalf("freshID row missing")
	}
	if gotFresh.State != cascade.NodeStateStale {
		t.Fatalf("fresh: state=%q want %q", gotFresh.State, cascade.NodeStateStale)
	}
	if gotFresh.FrameID == nil || *gotFresh.FrameID != cascadeFrameID {
		t.Fatalf("fresh: frame_id=%v want %v", gotFresh.FrameID, cascadeFrameID)
	}

	// stale-null node: state=stale, frame_id=cascadeFrameID
	var gotStaleNull *persistence.NodeRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.Nodes().Get(ctx, staleNullFrameID, tx)
		gotStaleNull = r
		return err
	}); err != nil {
		t.Fatalf("Get staleNullFrameID: %v", err)
	}
	if gotStaleNull == nil {
		t.Fatalf("staleNullFrameID row missing")
	}
	if gotStaleNull.State != cascade.NodeStateStale {
		t.Fatalf("stale-null: state=%q want %q", gotStaleNull.State, cascade.NodeStateStale)
	}
	if gotStaleNull.FrameID == nil || *gotStaleNull.FrameID != cascadeFrameID {
		t.Fatalf("stale-null: frame_id=%v want %v", gotStaleNull.FrameID, cascadeFrameID)
	}

	// running node: state=running, frame_id=otherFrameID (predicate excluded
	// the running row; the cascade must NOT have overwritten it).
	var gotRunning *persistence.NodeRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.Nodes().Get(ctx, runningID, tx)
		gotRunning = r
		return err
	}); err != nil {
		t.Fatalf("Get runningID: %v", err)
	}
	if gotRunning == nil {
		t.Fatalf("runningID row missing")
	}
	if gotRunning.State != cascade.NodeStateRunning {
		t.Fatalf("running: state=%q want %q (cascade must not overwrite running rows)",
			gotRunning.State, cascade.NodeStateRunning)
	}
	if gotRunning.FrameID == nil || *gotRunning.FrameID != otherFrameID {
		t.Fatalf("running: frame_id=%v want %v (cascade must not overwrite frame_id of running rows)",
			gotRunning.FrameID, otherFrameID)
	}
}
