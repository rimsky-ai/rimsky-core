// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

type fakeRunTreeTable struct {
	rows   map[shared.UUID]*persistence.NodeRunTreeRow
	scopes *fakeRunScopeTable
}

type fakeRunScopeTable struct {
	rows map[shared.UUID]*persistence.RunScopeRow
}

func newFakes() (*fakeRunTreeTable, *fakeRunScopeTable) {
	scopes := &fakeRunScopeTable{rows: make(map[shared.UUID]*persistence.RunScopeRow)}
	tree := &fakeRunTreeTable{
		rows:   make(map[shared.UUID]*persistence.NodeRunTreeRow),
		scopes: scopes,
	}
	return tree, scopes
}

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

func (f *fakeRunScopeTable) GetFanoutPartition(_ context.Context, _ persistence.Tx, parentNodeRunID shared.UUID, partitionKey string) (*persistence.RunScopeRow, error) {
	for _, r := range f.rows {
		if r.ParentNodeRunID != nil && *r.ParentNodeRunID == parentNodeRunID && r.PartitionKey == partitionKey && r.ClosedAt == nil {
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

func (f *fakeRunScopeTable) ListChildScopes(_ context.Context, _ persistence.Tx, parentNodeRunID shared.UUID) ([]persistence.RunScopeRow, error) {
	var out []persistence.RunScopeRow
	for _, r := range f.rows {
		if r.ParentNodeRunID != nil && *r.ParentNodeRunID == parentNodeRunID {
			out = append(out, *r)
		}
	}
	return out, nil
}

func (f *fakeRunScopeTable) ListTreeDeepestFirst(_ context.Context, _ persistence.Tx, rootRunScopeID shared.UUID) ([]persistence.RunScopeRow, error) {
	depthOf := func(id shared.UUID) int {
		d := 0
		cur := id
		for {
			row, ok := f.rows[cur]
			if !ok || row.ParentRunScopeID == nil {
				return d
			}
			cur = *row.ParentRunScopeID
			d++
		}
	}
	inTree := func(id shared.UUID) bool {
		cur := id
		for {
			if cur == rootRunScopeID {
				return true
			}
			row, ok := f.rows[cur]
			if !ok || row.ParentRunScopeID == nil {
				return false
			}
			cur = *row.ParentRunScopeID
		}
	}
	var out []persistence.RunScopeRow
	for id, r := range f.rows {
		if inTree(id) {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return depthOf(out[i].ID) > depthOf(out[j].ID) })
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

func (f *fakeRunTreeTable) CreateRootNodeRun(_ context.Context, _ persistence.Tx, in persistence.CreateRootNodeRunInput) error {
	f.rows[in.NodeRunID] = &persistence.NodeRunTreeRow{
		NodeRunID:         in.NodeRunID,
		NodeID:            in.NodeID,
		FrameID:           in.FrameID,
		RunScopeID:        in.RunScopeID,
		State:             cascade.NodeStateStale,
		AggregationPolicy: in.AggregationPolicy,
	}
	return nil
}

func (f *fakeRunTreeTable) CreateChildNodeRun(_ context.Context, _ persistence.Tx, in persistence.CreateChildNodeRunInput) error {
	f.rows[in.NodeRunID] = &persistence.NodeRunTreeRow{
		NodeRunID:         in.NodeRunID,
		NodeID:            in.NodeID,
		FrameID:           in.FrameID,
		RunScopeID:        in.RunScopeID,
		State:             cascade.NodeStateStale,
		AggregationPolicy: in.AggregationPolicy,
	}
	return nil
}

func (f *fakeRunTreeTable) GetByID(_ context.Context, _ persistence.Tx, runID shared.UUID) (*persistence.NodeRunTreeRow, error) {
	row, ok := f.rows[runID]
	if !ok {
		return nil, nil
	}
	c := *row
	return &c, nil
}

func (f *fakeRunTreeTable) LockTreeForUpdate(ctx context.Context, tx persistence.Tx, runID shared.UUID) (*persistence.NodeRunTreeRow, error) {
	return f.GetByID(ctx, tx, runID)
}

func (f *fakeRunTreeTable) ListChildren(_ context.Context, _ persistence.Tx, parentNodeRunID shared.UUID) ([]persistence.NodeRunTreeRow, error) {
	matchingScopes := make(map[shared.UUID]struct{})
	for _, s := range f.scopes.rows {
		if s.ParentNodeRunID != nil && *s.ParentNodeRunID == parentNodeRunID {
			matchingScopes[s.ID] = struct{}{}
		}
	}
	var out []persistence.NodeRunTreeRow
	for _, r := range f.rows {
		if _, ok := matchingScopes[r.RunScopeID]; ok {
			out = append(out, *r)
		}
	}
	return out, nil
}

func (f *fakeRunTreeTable) UpdateStateAndOutcome(_ context.Context, _ persistence.Tx, runID shared.UUID, state cascade.NodeState, settlingSignalType *string, changed bool) error {
	row, ok := f.rows[runID]
	if !ok {
		return nil
	}
	row.State = state
	if settlingSignalType != nil {
		v := *settlingSignalType
		row.SettlingSignalType = &v
	}
	row.Changed = changed
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

func (f *fakeRunScopeTable) makeChildScope(parentScopeID, parentNodeRunID shared.UUID, partition, graphName string) shared.UUID {
	id := newUUID()
	f.rows[id] = &persistence.RunScopeRow{
		ID:               id,
		ParentRunScopeID: &parentScopeID,
		ParentNodeRunID:  &parentNodeRunID,
		GraphName:        graphName,
		PartitionKey:     partition,
		CreatedAt:        time.Now(),
	}
	return id
}

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

func strPtr(v string) *string { return &v }

func TestPropagateFromChildState_LeafRoot(t *testing.T) {
	rt, scopes := newFakes()
	frame := newUUID()
	rootScope := scopes.makeRootScope("main", newUUID())
	root := newUUID()
	c1, c2 := newUUID(), newUUID()
	ctx := context.Background()

	if err := rt.CreateRootNodeRun(ctx, nil, persistence.CreateRootNodeRunInput{
		NodeRunID:         root,
		NodeID:            newUUID(),
		FrameID:           frame,
		RunScopeID:        rootScope,
		AggregationPolicy: spec.AggregationPolicy{Kind: "strict"},
	}); err != nil {
		t.Fatalf("CreateRootNodeRun: %v", err)
	}
	c1Scope := scopes.makeChildScope(rootScope, root, "a", "main")
	c2Scope := scopes.makeChildScope(rootScope, root, "b", "main")
	if err := rt.CreateChildNodeRun(ctx, nil, persistence.CreateChildNodeRunInput{
		NodeRunID: c1, NodeID: newUUID(), FrameID: frame, RunScopeID: c1Scope,
	}); err != nil {
		t.Fatalf("CreateChildNodeRun c1: %v", err)
	}
	if err := rt.CreateChildNodeRun(ctx, nil, persistence.CreateChildNodeRunInput{
		NodeRunID: c2, NodeID: newUUID(), FrameID: frame, RunScopeID: c2Scope,
	}); err != nil {
		t.Fatalf("CreateChildNodeRun c2: %v", err)
	}

	args := PropagationArgs{NodeRunTree: rt, RunScopes: scopes}
	successSig := strPtr("terminal/success")

	_ = rt.UpdateStateAndOutcome(ctx, nil, c1, cascade.NodeStateFresh, successSig, false)
	if _, _, err := PropagateFromChildState(ctx, args, nil, c1, cascade.NodeStateFresh, successSig); err != nil {
		t.Fatalf("PropagateFromChildState c1: %v", err)
	}
	rootRow, _ := rt.GetByID(ctx, nil, root)
	if rootRow.State != cascade.NodeStateStale {
		t.Fatalf("expected root still stale after one child, got %s", rootRow.State)
	}

	_ = rt.UpdateStateAndOutcome(ctx, nil, c2, cascade.NodeStateFresh, successSig, false)
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

func TestPropagateFromChildState_ParentChangedReflectsChildrenNotHardcodedTrue(t *testing.T) {
	rt, scopes := newFakes()
	frame := newUUID()
	rootScope := scopes.makeRootScope("main", newUUID())
	root := newUUID()
	c1, c2 := newUUID(), newUUID()
	ctx := context.Background()

	if err := rt.CreateRootNodeRun(ctx, nil, persistence.CreateRootNodeRunInput{
		NodeRunID:         root,
		NodeID:            newUUID(),
		FrameID:           frame,
		RunScopeID:        rootScope,
		AggregationPolicy: spec.AggregationPolicy{Kind: "strict"},
	}); err != nil {
		t.Fatalf("CreateRootNodeRun: %v", err)
	}
	c1Scope := scopes.makeChildScope(rootScope, root, "a", "main")
	c2Scope := scopes.makeChildScope(rootScope, root, "b", "main")
	if err := rt.CreateChildNodeRun(ctx, nil, persistence.CreateChildNodeRunInput{
		NodeRunID: c1, NodeID: newUUID(), FrameID: frame, RunScopeID: c1Scope,
	}); err != nil {
		t.Fatalf("CreateChildNodeRun c1: %v", err)
	}
	if err := rt.CreateChildNodeRun(ctx, nil, persistence.CreateChildNodeRunInput{
		NodeRunID: c2, NodeID: newUUID(), FrameID: frame, RunScopeID: c2Scope,
	}); err != nil {
		t.Fatalf("CreateChildNodeRun c2: %v", err)
	}

	args := PropagationArgs{NodeRunTree: rt, RunScopes: scopes}
	successSig := strPtr("terminal/success")

	if err := rt.UpdateStateAndOutcome(ctx, nil, c1, cascade.NodeStateFresh, successSig, false); err != nil {
		t.Fatalf("UpdateStateAndOutcome c1: %v", err)
	}
	if _, _, err := PropagateFromChildState(ctx, args, nil, c1, cascade.NodeStateFresh, successSig); err != nil {
		t.Fatalf("PropagateFromChildState c1: %v", err)
	}

	if err := rt.UpdateStateAndOutcome(ctx, nil, c2, cascade.NodeStateFresh, successSig, false); err != nil {
		t.Fatalf("UpdateStateAndOutcome c2: %v", err)
	}
	_, settlements, err := PropagateFromChildState(ctx, args, nil, c2, cascade.NodeStateFresh, successSig)
	if err != nil {
		t.Fatalf("PropagateFromChildState c2: %v", err)
	}
	if len(settlements) != 1 {
		t.Fatalf("expected exactly one parent settlement, got %d", len(settlements))
	}
	if settlements[0].NewChanged {
		t.Fatalf("both children settled with changed=false; the parent settlement must report changed=false, "+
			"not hardcode true — got NewChanged=%v", settlements[0].NewChanged)
	}
	rootRow, _ := rt.GetByID(ctx, nil, root)
	if rootRow.Changed {
		t.Fatalf("the persisted parent row must also carry changed=false, not the stale hardcoded true")
	}
}

func TestPropagateFromChildState_ParentChangedTrueWhenAnyChildChanged(t *testing.T) {
	rt, scopes := newFakes()
	frame := newUUID()
	rootScope := scopes.makeRootScope("main", newUUID())
	root := newUUID()
	c1, c2 := newUUID(), newUUID()
	ctx := context.Background()

	if err := rt.CreateRootNodeRun(ctx, nil, persistence.CreateRootNodeRunInput{
		NodeRunID:         root,
		NodeID:            newUUID(),
		FrameID:           frame,
		RunScopeID:        rootScope,
		AggregationPolicy: spec.AggregationPolicy{Kind: "strict"},
	}); err != nil {
		t.Fatalf("CreateRootNodeRun: %v", err)
	}
	c1Scope := scopes.makeChildScope(rootScope, root, "a", "main")
	c2Scope := scopes.makeChildScope(rootScope, root, "b", "main")
	if err := rt.CreateChildNodeRun(ctx, nil, persistence.CreateChildNodeRunInput{
		NodeRunID: c1, NodeID: newUUID(), FrameID: frame, RunScopeID: c1Scope,
	}); err != nil {
		t.Fatalf("CreateChildNodeRun c1: %v", err)
	}
	if err := rt.CreateChildNodeRun(ctx, nil, persistence.CreateChildNodeRunInput{
		NodeRunID: c2, NodeID: newUUID(), FrameID: frame, RunScopeID: c2Scope,
	}); err != nil {
		t.Fatalf("CreateChildNodeRun c2: %v", err)
	}

	args := PropagationArgs{NodeRunTree: rt, RunScopes: scopes}
	successSig := strPtr("terminal/success")

	if err := rt.UpdateStateAndOutcome(ctx, nil, c1, cascade.NodeStateFresh, successSig, false); err != nil {
		t.Fatalf("UpdateStateAndOutcome c1: %v", err)
	}
	if _, _, err := PropagateFromChildState(ctx, args, nil, c1, cascade.NodeStateFresh, successSig); err != nil {
		t.Fatalf("PropagateFromChildState c1: %v", err)
	}

	if err := rt.UpdateStateAndOutcome(ctx, nil, c2, cascade.NodeStateFresh, successSig, true); err != nil {
		t.Fatalf("UpdateStateAndOutcome c2: %v", err)
	}
	_, settlements, err := PropagateFromChildState(ctx, args, nil, c2, cascade.NodeStateFresh, successSig)
	if err != nil {
		t.Fatalf("PropagateFromChildState c2: %v", err)
	}
	if len(settlements) != 1 {
		t.Fatalf("expected exactly one parent settlement, got %d", len(settlements))
	}
	if !settlements[0].NewChanged {
		t.Fatalf("one child settled with changed=true; the parent settlement must report changed=true "+
			"(OR across children), got NewChanged=%v", settlements[0].NewChanged)
	}
	rootRow, _ := rt.GetByID(ctx, nil, root)
	if !rootRow.Changed {
		t.Fatalf("the persisted parent row must also carry changed=true")
	}
}

func TestPropagateFromChildState_StrictCancelSiblings(t *testing.T) {
	rt, scopes := newFakes()
	frame := newUUID()
	rootScope := scopes.makeRootScope("main", newUUID())
	root := newUUID()
	c1, c2 := newUUID(), newUUID()
	ctx := context.Background()

	_ = rt.CreateRootNodeRun(ctx, nil, persistence.CreateRootNodeRunInput{
		NodeRunID:         root,
		NodeID:            newUUID(),
		FrameID:           frame,
		RunScopeID:        rootScope,
		AggregationPolicy: spec.AggregationPolicy{Kind: "strict"},
	})
	c1Scope := scopes.makeChildScope(rootScope, root, "a", "main")
	c2Scope := scopes.makeChildScope(rootScope, root, "b", "main")
	_ = rt.CreateChildNodeRun(ctx, nil, persistence.CreateChildNodeRunInput{
		NodeRunID: c1, NodeID: newUUID(), FrameID: frame, RunScopeID: c1Scope,
	})
	_ = rt.CreateChildNodeRun(ctx, nil, persistence.CreateChildNodeRunInput{
		NodeRunID: c2, NodeID: newUUID(), FrameID: frame, RunScopeID: c2Scope,
	})
	_ = rt.UpdateStateAndOutcome(ctx, nil, c1, cascade.NodeStateRunning, nil, false)

	failedSig := strPtr("terminal/error/test_failure")
	_ = rt.UpdateStateAndOutcome(ctx, nil, c2, cascade.NodeStateFailed, failedSig, false)
	actions, _, err := PropagateFromChildState(context.Background(), PropagationArgs{NodeRunTree: rt, RunScopes: scopes}, nil,
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

func TestPropagateFromChildState_NestedTree(t *testing.T) {
	rt, scopes := newFakes()
	frame := newUUID()
	rootScope := scopes.makeRootScope("main", newUUID())
	root := newUUID()
	mid := newUUID()
	leaf1, leaf2 := newUUID(), newUUID()
	ctx := context.Background()

	_ = rt.CreateRootNodeRun(ctx, nil, persistence.CreateRootNodeRunInput{
		NodeRunID:         root,
		NodeID:            newUUID(),
		FrameID:           frame,
		RunScopeID:        rootScope,
		AggregationPolicy: spec.AggregationPolicy{Kind: "strict"},
	})
	midScope := scopes.makeChildScope(rootScope, root, "m", "main")
	_ = rt.CreateChildNodeRun(ctx, nil, persistence.CreateChildNodeRunInput{
		NodeRunID: mid, NodeID: newUUID(), FrameID: frame, RunScopeID: midScope,
		AggregationPolicy: spec.AggregationPolicy{Kind: "strict"},
	})
	leaf1Scope := scopes.makeChildScope(midScope, mid, "a", "main")
	leaf2Scope := scopes.makeChildScope(midScope, mid, "b", "main")
	_ = rt.CreateChildNodeRun(ctx, nil, persistence.CreateChildNodeRunInput{
		NodeRunID: leaf1, NodeID: newUUID(), FrameID: frame, RunScopeID: leaf1Scope,
	})
	_ = rt.CreateChildNodeRun(ctx, nil, persistence.CreateChildNodeRunInput{
		NodeRunID: leaf2, NodeID: newUUID(), FrameID: frame, RunScopeID: leaf2Scope,
	})

	args := PropagationArgs{NodeRunTree: rt, RunScopes: scopes}
	successSig := strPtr("terminal/success")

	_ = rt.UpdateStateAndOutcome(ctx, nil, leaf1, cascade.NodeStateFresh, successSig, false)
	if _, _, err := PropagateFromChildState(ctx, args, nil, leaf1,
		cascade.NodeStateFresh, successSig); err != nil {
		t.Fatalf("propagate leaf1: %v", err)
	}
	midRow, _ := rt.GetByID(ctx, nil, mid)
	if midRow.State != cascade.NodeStateStale {
		t.Fatalf("expected mid stale, got %s", midRow.State)
	}

	_ = rt.UpdateStateAndOutcome(ctx, nil, leaf2, cascade.NodeStateFresh, successSig, false)
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

func TestParentSettlementSignal_FailedArmIsSchemaConformant(t *testing.T) {
	sig := parentSettlementSignal(
		cascade.NodeStateFailed,
		signalpkg.TypePath("terminal/error/aggregate/strict_failed"),
		false,
	)
	if sig.Type != "terminal/error/aggregate/strict_failed" {
		t.Fatalf("type: got %q", sig.Type)
	}
	for _, k := range []string{"error_class", "error_payload", "attempt", "retries_so_far", "attributes_delta", "tags"} {
		if _, ok := sig.Payload[k]; !ok {
			t.Fatalf("payload missing schema key %q; payload=%+v", k, sig.Payload)
		}
	}
	if sig.Payload["error_class"] != "aggregate/strict_failed" {
		t.Errorf("error_class: got %v", sig.Payload["error_class"])
	}
}

func TestParentSettlementSignal_SuccessArmIsSchemaConformant(t *testing.T) {
	sig := parentSettlementSignal(
		cascade.NodeStateFresh,
		signalpkg.TypePath("terminal/success"),
		true,
	)
	if sig.Type != "terminal/success" {
		t.Fatalf("type: got %q", sig.Type)
	}
	for _, k := range []string{"changed", "attributes_delta", "change_summary", "tags"} {
		if _, ok := sig.Payload[k]; !ok {
			t.Fatalf("payload missing schema key %q; payload=%+v", k, sig.Payload)
		}
	}
	if sig.Payload["changed"] != true {
		t.Errorf("changed: got %v", sig.Payload["changed"])
	}
	if sig.Payload["change_summary"] != "aggregated_settlement" {
		t.Errorf("change_summary: got %v", sig.Payload["change_summary"])
	}
}

func TestParentSettlementSignal_ParkedStatePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic for unreachable Parked state")
		}
	}()
	_ = parentSettlementSignal(
		cascade.NodeStateParked,
		signalpkg.TypePath("transient/park"),
		false,
	)
}

func TestPropagateFromChildState_SettledParentReprojectedByLateChildTransition(t *testing.T) {
	rt, scopes := newFakes()
	frame := newUUID()
	rootScope := scopes.makeRootScope("main", newUUID())
	parent := newUUID()
	c1, c2 := newUUID(), newUUID()
	ctx := context.Background()

	_ = rt.CreateRootNodeRun(ctx, nil, persistence.CreateRootNodeRunInput{
		NodeRunID:         parent,
		NodeID:            newUUID(),
		FrameID:           frame,
		RunScopeID:        rootScope,
		AggregationPolicy: spec.AggregationPolicy{Kind: "strict"},
	})
	childScope := scopes.makeChildScope(rootScope, parent, "", "worker")
	_ = rt.CreateChildNodeRun(ctx, nil, persistence.CreateChildNodeRunInput{
		NodeRunID: c1, NodeID: newUUID(), FrameID: frame, RunScopeID: childScope,
	})
	_ = rt.CreateChildNodeRun(ctx, nil, persistence.CreateChildNodeRunInput{
		NodeRunID: c2, NodeID: newUUID(), FrameID: frame, RunScopeID: childScope,
	})

	successSig := strPtr("terminal/success")
	_ = rt.UpdateStateAndOutcome(ctx, nil, c1, cascade.NodeStateFresh, successSig, false)
	_ = rt.UpdateStateAndOutcome(ctx, nil, c2, cascade.NodeStateFresh, successSig, false)
	_ = rt.UpdateStateAndOutcome(ctx, nil, parent, cascade.NodeStateFresh, successSig, false)

	failedSig := strPtr("terminal/error/late_failure")
	_ = rt.UpdateStateAndOutcome(ctx, nil, c2, cascade.NodeStateFailed, failedSig, false)
	_, settlements, err := PropagateFromChildState(ctx, PropagationArgs{NodeRunTree: rt, RunScopes: scopes}, nil,
		c2, cascade.NodeStateFailed, failedSig)
	if err != nil {
		t.Fatalf("PropagateFromChildState after late child failure: %v", err)
	}
	parentRow, _ := rt.GetByID(ctx, nil, parent)
	if parentRow.State != cascade.NodeStateFailed {
		t.Fatalf("a settled (fresh) parent must be re-projected by a late child transition under "+
			"parent aggregation (terminal parents admit child_transitioned); got %s", parentRow.State)
	}
	if parentRow.SettlingSignalType == nil || *parentRow.SettlingSignalType != "terminal/error/aggregate/strict_failed" {
		t.Fatalf("re-projected parent must carry the strict_failed aggregate signal; got %v", parentRow.SettlingSignalType)
	}
	if len(settlements) != 1 {
		t.Fatalf("re-projection must append a parent settlement (cascade bridge + claim resolution feed off it); got %d", len(settlements))
	}
}
