// nodes_mark_stale_for_cascade.go — NodesMarkStaleForCascade conformance area.
//
// Covers NodeStore.MarkStaleForCascade — the cascade target used by the
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

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/shared"
)

func testNodesMarkStaleForCascade(t *testing.T, d persistence.Driver) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Store()

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

	// fresh node: created in 'fresh' by NodeStore.Create.
	if _, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
		ID:           freshID,
		InstanceID:   fix.InstanceID,
		NodeType:     "cascade-fresh",
		Executor:     "test-executor",
		Dependencies: []shared.UUID{},
	}, nil); err != nil {
		t.Fatalf("create fresh node: %v", err)
	}

	// stale node with NULL frame_id: created fresh, then transitioned to
	// stale via operator_invalidate, frame_id left at NULL.
	if _, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
		ID:           staleNullFrameID,
		InstanceID:   fix.InstanceID,
		NodeType:     "cascade-stale-null",
		Executor:     "test-executor",
		Dependencies: []shared.UUID{},
	}, nil); err != nil {
		t.Fatalf("create stale-null node: %v", err)
	}
	if err := store.Nodes().UpdateState(ctx, staleNullFrameID,
		shared.NodeStateStale, cascade.ReasonOperatorInvalidate, nil); err != nil {
		t.Fatalf("transition staleNull to stale: %v", err)
	}

	// running node: fresh -> stale -> running.
	if _, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
		ID:           runningID,
		InstanceID:   fix.InstanceID,
		NodeType:     "cascade-running",
		Executor:     "test-executor",
		Dependencies: []shared.UUID{},
	}, nil); err != nil {
		t.Fatalf("create running node: %v", err)
	}
	if err := store.Nodes().UpdateState(ctx, runningID,
		shared.NodeStateStale, cascade.ReasonOperatorInvalidate, nil); err != nil {
		t.Fatalf("running: fresh→stale: %v", err)
	}
	if err := store.Nodes().UpdateState(ctx, runningID,
		shared.NodeStateRunning, cascade.ReasonDispatchClaimed, nil); err != nil {
		t.Fatalf("running: stale→running: %v", err)
	}
	// Pin the running node's frame_id to a different frame; the cascade
	// must NOT overwrite it.
	if err := store.Nodes().SetFrameID(ctx, runningID, &otherFrameID, nil); err != nil {
		t.Fatalf("running: SetFrameID: %v", err)
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
	gotFresh, err := store.Nodes().Get(ctx, freshID, nil)
	if err != nil {
		t.Fatalf("Get freshID: %v", err)
	}
	if gotFresh == nil {
		t.Fatalf("freshID row missing")
	}
	if gotFresh.State != shared.NodeStateStale {
		t.Fatalf("fresh: state=%q want %q", gotFresh.State, shared.NodeStateStale)
	}
	if gotFresh.FrameID == nil || *gotFresh.FrameID != cascadeFrameID {
		t.Fatalf("fresh: frame_id=%v want %v", gotFresh.FrameID, cascadeFrameID)
	}

	// stale-null node: state=stale, frame_id=cascadeFrameID
	gotStaleNull, err := store.Nodes().Get(ctx, staleNullFrameID, nil)
	if err != nil {
		t.Fatalf("Get staleNullFrameID: %v", err)
	}
	if gotStaleNull == nil {
		t.Fatalf("staleNullFrameID row missing")
	}
	if gotStaleNull.State != shared.NodeStateStale {
		t.Fatalf("stale-null: state=%q want %q", gotStaleNull.State, shared.NodeStateStale)
	}
	if gotStaleNull.FrameID == nil || *gotStaleNull.FrameID != cascadeFrameID {
		t.Fatalf("stale-null: frame_id=%v want %v", gotStaleNull.FrameID, cascadeFrameID)
	}

	// running node: state=running, frame_id=otherFrameID (predicate excluded
	// the running row; the cascade must NOT have overwritten it).
	gotRunning, err := store.Nodes().Get(ctx, runningID, nil)
	if err != nil {
		t.Fatalf("Get runningID: %v", err)
	}
	if gotRunning == nil {
		t.Fatalf("runningID row missing")
	}
	if gotRunning.State != shared.NodeStateRunning {
		t.Fatalf("running: state=%q want %q (cascade must not overwrite running rows)",
			gotRunning.State, shared.NodeStateRunning)
	}
	if gotRunning.FrameID == nil || *gotRunning.FrameID != otherFrameID {
		t.Fatalf("running: frame_id=%v want %v (cascade must not overwrite frame_id of running rows)",
			gotRunning.FrameID, otherFrameID)
	}
}
