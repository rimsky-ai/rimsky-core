// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/foundation/spec"
)

// fakeRunTreeTable is an in-memory persistence.RunTreeTable used by the
// state-propagation tests. Keyed on RunID; ListChildren scans every row.
type fakeRunTreeTable struct {
	rows map[shared.UUID]*persistence.RunTreeRow
}

func newFakeRunTree() *fakeRunTreeTable {
	return &fakeRunTreeTable{rows: make(map[shared.UUID]*persistence.RunTreeRow)}
}

func (f *fakeRunTreeTable) CreateRootRun(_ context.Context, _ persistence.Tx, in persistence.CreateRootRunInput) error {
	f.rows[in.RunID] = &persistence.RunTreeRow{
		RunID:             in.RunID,
		NodeID:            in.NodeID,
		FrameID:           in.FrameID,
		State:             cascade.NodeStateStale,
		LastOutcome:       cascade.LastOutcomeFreshUnchanged,
		AggregationPolicy: in.AggregationPolicy,
	}
	return nil
}

func (f *fakeRunTreeTable) CreateChildRun(_ context.Context, _ persistence.Tx, in persistence.CreateChildRunInput) error {
	parent := in.ParentRunID
	f.rows[in.RunID] = &persistence.RunTreeRow{
		RunID:             in.RunID,
		NodeID:            in.NodeID,
		FrameID:           in.FrameID,
		ParentRunID:       &parent,
		ChildKey:          in.ChildKey,
		State:             cascade.NodeStateStale,
		LastOutcome:       cascade.LastOutcomeFreshUnchanged,
		AggregationPolicy: in.AggregationPolicy,
	}
	return nil
}

func (f *fakeRunTreeTable) GetByID(_ context.Context, _ persistence.Tx, runID shared.UUID) (*persistence.RunTreeRow, error) {
	row, ok := f.rows[runID]
	if !ok {
		return nil, nil
	}
	copy := *row
	return &copy, nil
}

func (f *fakeRunTreeTable) GetByParentChildKey(_ context.Context, _ persistence.Tx, parentRunID shared.UUID, childKey string) (*persistence.RunTreeRow, error) {
	for _, r := range f.rows {
		if r.ParentRunID != nil && *r.ParentRunID == parentRunID && r.ChildKey == childKey {
			copy := *r
			return &copy, nil
		}
	}
	return nil, nil
}

func (f *fakeRunTreeTable) LockTreeForUpdate(ctx context.Context, tx persistence.Tx, runID shared.UUID) (*persistence.RunTreeRow, error) {
	return f.GetByID(ctx, tx, runID)
}

func (f *fakeRunTreeTable) ListChildren(_ context.Context, _ persistence.Tx, parentRunID shared.UUID) ([]persistence.RunTreeRow, error) {
	var out []persistence.RunTreeRow
	for _, r := range f.rows {
		if r.ParentRunID != nil && *r.ParentRunID == parentRunID {
			out = append(out, *r)
		}
	}
	return out, nil
}

func (f *fakeRunTreeTable) UpdateStateAndOutcome(_ context.Context, _ persistence.Tx, runID shared.UUID, state cascade.NodeState, lastOutcome cascade.LastOutcome) error {
	row, ok := f.rows[runID]
	if !ok {
		return nil
	}
	row.State = state
	if lastOutcome != "" {
		row.LastOutcome = lastOutcome
	}
	return nil
}

func (f *fakeRunTreeTable) UpdateAggregationPolicy(_ context.Context, _ persistence.Tx, runID shared.UUID, policy spec.AggregationPolicy) error {
	row, ok := f.rows[runID]
	if !ok {
		return nil
	}
	row.AggregationPolicy = policy
	return nil
}

func newUUID() shared.UUID { return shared.UUID(uuid.New()) }

// TestPropagateFromChildState_LeafRoot — single-level fan-out, two children
// success → parent fresh + fresh_unchanged.
func TestPropagateFromChildState_LeafRoot(t *testing.T) {
	rt := newFakeRunTree()
	frame := newUUID()
	root := newUUID()
	c1, c2 := newUUID(), newUUID()
	ctx := context.Background()

	if err := rt.CreateRootRun(ctx, nil, persistence.CreateRootRunInput{
		RunID:             root,
		NodeID:            newUUID(),
		FrameID:           frame,
		AggregationPolicy: spec.AggregationPolicy{Kind: "strict"},
	}); err != nil {
		t.Fatalf("CreateRootRun: %v", err)
	}
	if err := rt.CreateChildRun(ctx, nil, persistence.CreateChildRunInput{
		RunID: c1, NodeID: newUUID(), FrameID: frame, ParentRunID: root, ChildKey: "a",
	}); err != nil {
		t.Fatalf("CreateChildRun c1: %v", err)
	}
	if err := rt.CreateChildRun(ctx, nil, persistence.CreateChildRunInput{
		RunID: c2, NodeID: newUUID(), FrameID: frame, ParentRunID: root, ChildKey: "b",
	}); err != nil {
		t.Fatalf("CreateChildRun c2: %v", err)
	}

	args := PropagationArgs{RunTree: rt}

	// First child terminates → parent still running (other child active).
	// Caller writes the child's state, then the walker rolls up.
	_ = rt.UpdateStateAndOutcome(ctx, nil, c1, cascade.NodeStateFresh, cascade.LastOutcomeFreshChanged)
	if _, err := PropagateFromChildState(ctx, args, nil, c1, cascade.NodeStateFresh, cascade.LastOutcomeFreshChanged); err != nil {
		t.Fatalf("PropagateFromChildState c1: %v", err)
	}
	rootRow, _ := rt.GetByID(ctx, nil, root)
	if rootRow.State != cascade.NodeStateStale {
		t.Fatalf("expected root still stale after one child, got %s", rootRow.State)
	}

	// Second child terminates → parent fresh + fresh_changed (the any-changed gate).
	_ = rt.UpdateStateAndOutcome(ctx, nil, c2, cascade.NodeStateFresh, cascade.LastOutcomeFreshUnchanged)
	actions, err := PropagateFromChildState(ctx, args, nil, c2, cascade.NodeStateFresh, cascade.LastOutcomeFreshUnchanged)
	if err != nil {
		t.Fatalf("PropagateFromChildState c2: %v", err)
	}
	rootRow, _ = rt.GetByID(ctx, nil, root)
	if rootRow.State != cascade.NodeStateFresh {
		t.Fatalf("expected root fresh, got %s", rootRow.State)
	}
	if rootRow.LastOutcome != cascade.LastOutcomeFreshChanged {
		t.Fatalf("expected root fresh_changed (any-changed propagates), got %s", rootRow.LastOutcome)
	}
	if len(actions) != 0 {
		t.Fatalf("expected no cancel actions, got %d", len(actions))
	}
}

// TestPropagateFromChildState_StrictCancelSiblings — first failure under
// strict.cancel_siblings → parent failed + AggregateActionCancelSiblings.
func TestPropagateFromChildState_StrictCancelSiblings(t *testing.T) {
	rt := newFakeRunTree()
	frame := newUUID()
	root := newUUID()
	c1, c2 := newUUID(), newUUID()
	ctx := context.Background()

	_ = rt.CreateRootRun(ctx, nil, persistence.CreateRootRunInput{
		RunID:             root,
		NodeID:            newUUID(),
		FrameID:           frame,
		AggregationPolicy: spec.AggregationPolicy{Kind: "strict", CancelSiblings: true},
	})
	_ = rt.CreateChildRun(ctx, nil, persistence.CreateChildRunInput{
		RunID: c1, NodeID: newUUID(), FrameID: frame, ParentRunID: root, ChildKey: "a",
	})
	_ = rt.CreateChildRun(ctx, nil, persistence.CreateChildRunInput{
		RunID: c2, NodeID: newUUID(), FrameID: frame, ParentRunID: root, ChildKey: "b",
	})
	// c1 still running.
	_ = rt.UpdateStateAndOutcome(ctx, nil, c1, cascade.NodeStateRunning, "")

	// c2 fails → parent failed + cancel-siblings action.
	// Caller writes the child terminal state, then the walker rolls up.
	_ = rt.UpdateStateAndOutcome(ctx, nil, c2, cascade.NodeStateFailed, cascade.LastOutcomeFailed)
	actions, err := PropagateFromChildState(context.Background(), PropagationArgs{RunTree: rt}, nil,
		c2, cascade.NodeStateFailed, cascade.LastOutcomeFailed)
	if err != nil {
		t.Fatalf("PropagateFromChildState c2: %v", err)
	}
	rootRow, _ := rt.GetByID(ctx, nil, root)
	if rootRow.State != cascade.NodeStateFailed {
		t.Fatalf("expected root failed, got %s", rootRow.State)
	}
	if len(actions) != 1 {
		t.Fatalf("expected 1 cancel action, got %d", len(actions))
	}
	if actions[0].Kind != AggregateActionCancelSiblings {
		t.Fatalf("expected cancel_siblings action, got %v", actions[0].Kind)
	}
}

// TestPropagateFromChildState_NestedTree — three-level tree: leaf →
// mid-parent → root. A leaf success rolls up through the mid-parent
// then to the root.
func TestPropagateFromChildState_NestedTree(t *testing.T) {
	rt := newFakeRunTree()
	frame := newUUID()
	root := newUUID()
	mid := newUUID()
	leaf1, leaf2 := newUUID(), newUUID()
	ctx := context.Background()

	_ = rt.CreateRootRun(ctx, nil, persistence.CreateRootRunInput{
		RunID:             root,
		NodeID:            newUUID(),
		FrameID:           frame,
		AggregationPolicy: spec.AggregationPolicy{Kind: "strict"},
	})
	_ = rt.CreateChildRun(ctx, nil, persistence.CreateChildRunInput{
		RunID: mid, NodeID: newUUID(), FrameID: frame, ParentRunID: root, ChildKey: "m",
		AggregationPolicy: spec.AggregationPolicy{Kind: "strict"},
	})
	_ = rt.CreateChildRun(ctx, nil, persistence.CreateChildRunInput{
		RunID: leaf1, NodeID: newUUID(), FrameID: frame, ParentRunID: mid, ChildKey: "a",
	})
	_ = rt.CreateChildRun(ctx, nil, persistence.CreateChildRunInput{
		RunID: leaf2, NodeID: newUUID(), FrameID: frame, ParentRunID: mid, ChildKey: "b",
	})

	args := PropagationArgs{RunTree: rt}

	// Caller writes the leaf's terminal state, then the walker rolls up.
	_ = rt.UpdateStateAndOutcome(ctx, nil, leaf1,
		cascade.NodeStateFresh, cascade.LastOutcomeFreshChanged)
	if _, err := PropagateFromChildState(ctx, args, nil, leaf1,
		cascade.NodeStateFresh, cascade.LastOutcomeFreshChanged); err != nil {
		t.Fatalf("propagate leaf1: %v", err)
	}
	// mid still stale (leaf2 active).
	midRow, _ := rt.GetByID(ctx, nil, mid)
	if midRow.State != cascade.NodeStateStale {
		t.Fatalf("expected mid stale, got %s", midRow.State)
	}

	_ = rt.UpdateStateAndOutcome(ctx, nil, leaf2,
		cascade.NodeStateFresh, cascade.LastOutcomeFreshUnchanged)
	if _, err := PropagateFromChildState(ctx, args, nil, leaf2,
		cascade.NodeStateFresh, cascade.LastOutcomeFreshUnchanged); err != nil {
		t.Fatalf("propagate leaf2: %v", err)
	}
	midRow, _ = rt.GetByID(ctx, nil, mid)
	if midRow.State != cascade.NodeStateFresh {
		t.Fatalf("expected mid fresh, got %s", midRow.State)
	}
	rootRow, _ := rt.GetByID(ctx, nil, root)
	if rootRow.State != cascade.NodeStateFresh {
		t.Fatalf("expected root fresh, got %s", rootRow.State)
	}
	if rootRow.LastOutcome != cascade.LastOutcomeFreshChanged {
		t.Fatalf("expected root fresh_changed (propagated from mid), got %s", rootRow.LastOutcome)
	}
}
