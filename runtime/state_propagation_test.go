// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/foundation/spec"
)

// fakeRunTreeTable + fakeRunScopeTable are in-memory persistence accessors
// used by the state-propagation tests. Keyed on RunID (RunTree) and
// RunScopeID (RunScope). ListChildren walks via RunScopes whose
// parent_run_id matches.
//
// The pair models the post-2026-05-22 reshape: a run row carries its
// owning RunScopeID; the RunScope carries the (parent_run_id,
// partition_key) tuple.
type fakeRunTreeTable struct {
	rows   map[shared.UUID]*persistence.RunTreeRow
	scopes *fakeRunScopeTable
}

type fakeRunScopeTable struct {
	rows map[shared.UUID]*persistence.RunScopeRow
}

func newFakes() (*fakeRunTreeTable, *fakeRunScopeTable) {
	scopes := &fakeRunScopeTable{rows: make(map[shared.UUID]*persistence.RunScopeRow)}
	tree := &fakeRunTreeTable{
		rows:   make(map[shared.UUID]*persistence.RunTreeRow),
		scopes: scopes,
	}
	return tree, scopes
}

// --- fakeRunScopeTable --- //

func (f *fakeRunScopeTable) Create(_ context.Context, _ persistence.Tx, row persistence.RunScopeRow) error {
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now()
	}
	f.rows[row.ID] = &row
	return nil
}

func (f *fakeRunScopeTable) GetByID(_ context.Context, _ persistence.Tx, id shared.UUID) (*persistence.RunScopeRow, error) {
	row, ok := f.rows[id]
	if !ok {
		return nil, nil
	}
	c := *row
	return &c, nil
}

func (f *fakeRunScopeTable) GetFanoutPartition(_ context.Context, _ persistence.Tx, parentRunID shared.UUID, partitionKey string) (*persistence.RunScopeRow, error) {
	for _, r := range f.rows {
		if r.ParentRunID != nil && *r.ParentRunID == parentRunID && r.PartitionKey == partitionKey && r.ClosedAt == nil {
			c := *r
			return &c, nil
		}
	}
	return nil, nil
}

func (f *fakeRunScopeTable) Close(_ context.Context, _ persistence.Tx, id shared.UUID) error {
	row, ok := f.rows[id]
	if !ok {
		return nil
	}
	if row.ClosedAt == nil {
		now := time.Now()
		row.ClosedAt = &now
	}
	return nil
}

func (f *fakeRunScopeTable) ListChildScopes(_ context.Context, _ persistence.Tx, parentRunID shared.UUID) ([]persistence.RunScopeRow, error) {
	var out []persistence.RunScopeRow
	for _, r := range f.rows {
		if r.ParentRunID != nil && *r.ParentRunID == parentRunID {
			out = append(out, *r)
		}
	}
	return out, nil
}

func (f *fakeRunScopeTable) ListParentChain(_ context.Context, _ persistence.Tx, id shared.UUID) ([]persistence.RunScopeRow, error) {
	var out []persistence.RunScopeRow
	cur := id
	for {
		row, ok := f.rows[cur]
		if !ok {
			break
		}
		out = append(out, *row)
		if row.ParentRunScopeID == nil {
			break
		}
		cur = *row.ParentRunScopeID
	}
	return out, nil
}

// --- fakeRunTreeTable --- //

func (f *fakeRunTreeTable) CreateRootRun(_ context.Context, _ persistence.Tx, in persistence.CreateRootRunInput) error {
	f.rows[in.RunID] = &persistence.RunTreeRow{
		RunID:             in.RunID,
		NodeID:            in.NodeID,
		FrameID:           in.FrameID,
		RunScopeID:        in.RunScopeID,
		State:             cascade.NodeStateStale,
		AggregationPolicy: in.AggregationPolicy,
	}
	return nil
}

func (f *fakeRunTreeTable) CreateChildRun(_ context.Context, _ persistence.Tx, in persistence.CreateChildRunInput) error {
	f.rows[in.RunID] = &persistence.RunTreeRow{
		RunID:             in.RunID,
		NodeID:            in.NodeID,
		FrameID:           in.FrameID,
		RunScopeID:        in.RunScopeID,
		State:             cascade.NodeStateStale,
		AggregationPolicy: in.AggregationPolicy,
	}
	return nil
}

func (f *fakeRunTreeTable) GetByID(_ context.Context, _ persistence.Tx, runID shared.UUID) (*persistence.RunTreeRow, error) {
	row, ok := f.rows[runID]
	if !ok {
		return nil, nil
	}
	c := *row
	return &c, nil
}

func (f *fakeRunTreeTable) LockTreeForUpdate(ctx context.Context, tx persistence.Tx, runID shared.UUID) (*persistence.RunTreeRow, error) {
	return f.GetByID(ctx, tx, runID)
}

// ListChildren returns all run rows whose owning RunScope's parent_run_id
// equals parentRunID — the new shape after the inline parent_run_id /
// child_key columns moved onto rimsky_run_scopes.
func (f *fakeRunTreeTable) ListChildren(_ context.Context, _ persistence.Tx, parentRunID shared.UUID) ([]persistence.RunTreeRow, error) {
	matchingScopes := make(map[shared.UUID]struct{})
	for _, s := range f.scopes.rows {
		if s.ParentRunID != nil && *s.ParentRunID == parentRunID {
			matchingScopes[s.ID] = struct{}{}
		}
	}
	var out []persistence.RunTreeRow
	for _, r := range f.rows {
		if _, ok := matchingScopes[r.RunScopeID]; ok {
			out = append(out, *r)
		}
	}
	return out, nil
}

func (f *fakeRunTreeTable) UpdateStateAndOutcome(_ context.Context, _ persistence.Tx, runID shared.UUID, state cascade.NodeState, settlingSignalType *string) error {
	row, ok := f.rows[runID]
	if !ok {
		return nil
	}
	row.State = state
	if settlingSignalType != nil {
		v := *settlingSignalType
		row.SettlingSignalType = &v
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

// makeChildScope creates a fan-out partition RunScope under parentRunID
// and returns its id.
func (f *fakeRunScopeTable) makeChildScope(parentScopeID, parentRunID shared.UUID, partition, graphName string) shared.UUID {
	id := newUUID()
	f.rows[id] = &persistence.RunScopeRow{
		ID:               id,
		ParentRunScopeID: &parentScopeID,
		ParentRunID:      &parentRunID,
		GraphName:        graphName,
		PartitionKey:     partition,
		CreatedAt:        time.Now(),
	}
	return id
}

// makeRootScope creates the main RunScope.
func (f *fakeRunScopeTable) makeRootScope(graphName string, instanceID shared.UUID) shared.UUID {
	id := newUUID()
	f.rows[id] = &persistence.RunScopeRow{
		ID:         id,
		GraphName:  graphName,
		InstanceID: instanceID,
		CreatedAt:  time.Now(),
	}
	return id
}

// strPtr returns a *string pointing to v.
func strPtr(v string) *string { return &v }

// TestPropagateFromChildState_LeafRoot — single-level fan-out, two children
// success → parent fresh + terminal/success.
func TestPropagateFromChildState_LeafRoot(t *testing.T) {
	rt, scopes := newFakes()
	frame := newUUID()
	rootScope := scopes.makeRootScope("main", newUUID())
	root := newUUID()
	c1, c2 := newUUID(), newUUID()
	ctx := context.Background()

	if err := rt.CreateRootRun(ctx, nil, persistence.CreateRootRunInput{
		RunID:             root,
		NodeID:            newUUID(),
		FrameID:           frame,
		RunScopeID:        rootScope,
		AggregationPolicy: spec.AggregationPolicy{Kind: "strict"},
	}); err != nil {
		t.Fatalf("CreateRootRun: %v", err)
	}
	// Two fan-out partition RunScopes under root.
	c1Scope := scopes.makeChildScope(rootScope, root, "a", "main")
	c2Scope := scopes.makeChildScope(rootScope, root, "b", "main")
	if err := rt.CreateChildRun(ctx, nil, persistence.CreateChildRunInput{
		RunID: c1, NodeID: newUUID(), FrameID: frame, RunScopeID: c1Scope,
	}); err != nil {
		t.Fatalf("CreateChildRun c1: %v", err)
	}
	if err := rt.CreateChildRun(ctx, nil, persistence.CreateChildRunInput{
		RunID: c2, NodeID: newUUID(), FrameID: frame, RunScopeID: c2Scope,
	}); err != nil {
		t.Fatalf("CreateChildRun c2: %v", err)
	}

	args := PropagationArgs{RunTree: rt, RunScopes: scopes}
	successSig := strPtr("terminal/success")

	// First child terminates → parent still stale (other child active).
	_ = rt.UpdateStateAndOutcome(ctx, nil, c1, cascade.NodeStateFresh, successSig)
	if _, _, err := PropagateFromChildState(ctx, args, nil, c1, cascade.NodeStateFresh, successSig); err != nil {
		t.Fatalf("PropagateFromChildState c1: %v", err)
	}
	rootRow, _ := rt.GetByID(ctx, nil, root)
	if rootRow.State != cascade.NodeStateStale {
		t.Fatalf("expected root still stale after one child, got %s", rootRow.State)
	}

	// Second child terminates → parent fresh + terminal/success.
	_ = rt.UpdateStateAndOutcome(ctx, nil, c2, cascade.NodeStateFresh, successSig)
	actions, _, err := PropagateFromChildState(ctx, args, nil, c2, cascade.NodeStateFresh, successSig)
	if err != nil {
		t.Fatalf("PropagateFromChildState c2: %v", err)
	}
	rootRow, _ = rt.GetByID(ctx, nil, root)
	if rootRow.State != cascade.NodeStateFresh {
		t.Fatalf("expected root fresh, got %s", rootRow.State)
	}
	if rootRow.SettlingSignalType == nil || *rootRow.SettlingSignalType != "terminal/success" {
		t.Fatalf("expected root settling_signal_type=terminal/success; got %v", rootRow.SettlingSignalType)
	}
	if len(actions) != 0 {
		t.Fatalf("expected no cancel actions, got %d", len(actions))
	}
}

// TestPropagateFromChildState_StrictCancelSiblings — first failure under
// strict.cancel_siblings → parent failed + AggregateActionCancelSiblings.
func TestPropagateFromChildState_StrictCancelSiblings(t *testing.T) {
	rt, scopes := newFakes()
	frame := newUUID()
	rootScope := scopes.makeRootScope("main", newUUID())
	root := newUUID()
	c1, c2 := newUUID(), newUUID()
	ctx := context.Background()

	_ = rt.CreateRootRun(ctx, nil, persistence.CreateRootRunInput{
		RunID:             root,
		NodeID:            newUUID(),
		FrameID:           frame,
		RunScopeID:        rootScope,
		AggregationPolicy: spec.AggregationPolicy{Kind: "strict", CancelSiblings: true},
	})
	c1Scope := scopes.makeChildScope(rootScope, root, "a", "main")
	c2Scope := scopes.makeChildScope(rootScope, root, "b", "main")
	_ = rt.CreateChildRun(ctx, nil, persistence.CreateChildRunInput{
		RunID: c1, NodeID: newUUID(), FrameID: frame, RunScopeID: c1Scope,
	})
	_ = rt.CreateChildRun(ctx, nil, persistence.CreateChildRunInput{
		RunID: c2, NodeID: newUUID(), FrameID: frame, RunScopeID: c2Scope,
	})
	// c1 still running.
	_ = rt.UpdateStateAndOutcome(ctx, nil, c1, cascade.NodeStateRunning, nil)

	// c2 fails → parent failed + cancel-siblings action.
	failedSig := strPtr("terminal/error/test_failure")
	_ = rt.UpdateStateAndOutcome(ctx, nil, c2, cascade.NodeStateFailed, failedSig)
	actions, _, err := PropagateFromChildState(context.Background(), PropagationArgs{RunTree: rt, RunScopes: scopes}, nil,
		c2, cascade.NodeStateFailed, failedSig)
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
	rt, scopes := newFakes()
	frame := newUUID()
	rootScope := scopes.makeRootScope("main", newUUID())
	root := newUUID()
	mid := newUUID()
	leaf1, leaf2 := newUUID(), newUUID()
	ctx := context.Background()

	_ = rt.CreateRootRun(ctx, nil, persistence.CreateRootRunInput{
		RunID:             root,
		NodeID:            newUUID(),
		FrameID:           frame,
		RunScopeID:        rootScope,
		AggregationPolicy: spec.AggregationPolicy{Kind: "strict"},
	})
	midScope := scopes.makeChildScope(rootScope, root, "m", "main")
	_ = rt.CreateChildRun(ctx, nil, persistence.CreateChildRunInput{
		RunID: mid, NodeID: newUUID(), FrameID: frame, RunScopeID: midScope,
		AggregationPolicy: spec.AggregationPolicy{Kind: "strict"},
	})
	leaf1Scope := scopes.makeChildScope(midScope, mid, "a", "main")
	leaf2Scope := scopes.makeChildScope(midScope, mid, "b", "main")
	_ = rt.CreateChildRun(ctx, nil, persistence.CreateChildRunInput{
		RunID: leaf1, NodeID: newUUID(), FrameID: frame, RunScopeID: leaf1Scope,
	})
	_ = rt.CreateChildRun(ctx, nil, persistence.CreateChildRunInput{
		RunID: leaf2, NodeID: newUUID(), FrameID: frame, RunScopeID: leaf2Scope,
	})

	args := PropagationArgs{RunTree: rt, RunScopes: scopes}
	successSig := strPtr("terminal/success")

	_ = rt.UpdateStateAndOutcome(ctx, nil, leaf1, cascade.NodeStateFresh, successSig)
	if _, _, err := PropagateFromChildState(ctx, args, nil, leaf1,
		cascade.NodeStateFresh, successSig); err != nil {
		t.Fatalf("propagate leaf1: %v", err)
	}
	midRow, _ := rt.GetByID(ctx, nil, mid)
	if midRow.State != cascade.NodeStateStale {
		t.Fatalf("expected mid stale, got %s", midRow.State)
	}

	_ = rt.UpdateStateAndOutcome(ctx, nil, leaf2, cascade.NodeStateFresh, successSig)
	if _, _, err := PropagateFromChildState(ctx, args, nil, leaf2,
		cascade.NodeStateFresh, successSig); err != nil {
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
	if rootRow.SettlingSignalType == nil || *rootRow.SettlingSignalType != "terminal/success" {
		t.Fatalf("expected root settling_signal_type=terminal/success; got %v", rootRow.SettlingSignalType)
	}
}
