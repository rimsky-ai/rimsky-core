// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N3 scenario — exit_carry_rule.
//
// At exit's leaf-run terminal, the supervisor copies exit's writeback
// to the parent run's writeback row in the same transaction as exit's
// terminal write. Per spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Sub-graphs / Aggregation / Writeback carry-rule for exit.
//
// This is a unit-level smoke against the `CarryExitWriteback` helper:
// it consults the run-tree row + RunScope to locate the parent and
// validates the writeback bytes JSON-decode. Per @blessed-invariant 20
// the helper does not mangle bytes — it round-trips through json.Unmarshal
// only to enforce the schema contract.
package subgraph

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	persistence "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

// fakeRunTreeForExit is the minimal RunTreeTable stand-in
// CarryExitWriteback consults. We only need GetByID; other methods
// return zero values so the interface satisfies the persistence
// contract.
type fakeRunTreeForExit struct {
	rows map[shared.UUID]*persistence.RunTreeRow
}

func (f *fakeRunTreeForExit) CreateRootRun(ctx context.Context, tx persistence.Tx, in persistence.CreateRootRunInput) error {
	return nil
}
func (f *fakeRunTreeForExit) CreateChildRun(ctx context.Context, tx persistence.Tx, in persistence.CreateChildRunInput) error {
	return nil
}
func (f *fakeRunTreeForExit) GetByID(ctx context.Context, tx persistence.Tx, runID shared.UUID) (*persistence.RunTreeRow, error) {
	return f.rows[runID], nil
}
func (f *fakeRunTreeForExit) LockTreeForUpdate(ctx context.Context, tx persistence.Tx, runID shared.UUID) (*persistence.RunTreeRow, error) {
	return f.rows[runID], nil
}
func (f *fakeRunTreeForExit) ListChildren(ctx context.Context, tx persistence.Tx, parentRunID shared.UUID) ([]persistence.RunTreeRow, error) {
	return nil, nil
}
func (f *fakeRunTreeForExit) UpdateStateAndOutcome(ctx context.Context, tx persistence.Tx, runID shared.UUID, state cascade.NodeState, settlingSignalType *string) error {
	return nil
}
func (f *fakeRunTreeForExit) UpdateAggregationPolicy(ctx context.Context, tx persistence.Tx, runID shared.UUID, policy tmplspec.AggregationPolicy) error {
	return nil
}

// fakeRunScopeForExit is the minimal RunScopeTable stand-in
// CarryExitWriteback consults via args.RunScopes.GetByID. Keyed on
// RunScopeID.
type fakeRunScopeForExit struct {
	rows map[shared.UUID]*persistence.RunScopeRow
}

func (f *fakeRunScopeForExit) Create(context.Context, persistence.Tx, persistence.RunScopeRow) error {
	return nil
}
func (f *fakeRunScopeForExit) GetByID(_ context.Context, _ persistence.Tx, id shared.UUID) (*persistence.RunScopeRow, error) {
	return f.rows[id], nil
}
func (f *fakeRunScopeForExit) GetFanoutPartition(context.Context, persistence.Tx, shared.UUID, string) (*persistence.RunScopeRow, error) {
	return nil, nil
}
func (f *fakeRunScopeForExit) Close(context.Context, persistence.Tx, shared.UUID) error {
	return nil
}
func (f *fakeRunScopeForExit) ListChildScopes(context.Context, persistence.Tx, shared.UUID) ([]persistence.RunScopeRow, error) {
	return nil, nil
}
func (f *fakeRunScopeForExit) ListParentChain(context.Context, persistence.Tx, shared.UUID) ([]persistence.RunScopeRow, error) {
	return nil, nil
}

// makeFixture builds a (RunTree, RunScopes) pair where exit is in a
// child RunScope whose parent_run_id points at parentID.
func makeFixture(parentID, exitID shared.UUID) (*fakeRunTreeForExit, *fakeRunScopeForExit) {
	parentScopeID := shared.UUID(uuid.New())
	exitScopeID := shared.UUID(uuid.New())
	parent := &persistence.RunTreeRow{RunID: parentID, NodeID: shared.UUID(uuid.New()), RunScopeID: parentScopeID, State: cascade.NodeStateRunning}
	exit := &persistence.RunTreeRow{RunID: exitID, NodeID: shared.UUID(uuid.New()), RunScopeID: exitScopeID, State: cascade.NodeStateRunning}
	rt := &fakeRunTreeForExit{rows: map[shared.UUID]*persistence.RunTreeRow{parentID: parent, exitID: exit}}
	scopes := &fakeRunScopeForExit{rows: map[shared.UUID]*persistence.RunScopeRow{
		parentScopeID: {ID: parentScopeID, GraphName: "main"},
		exitScopeID:   {ID: exitScopeID, ParentRunScopeID: &parentScopeID, ParentRunID: &parentID, GraphName: "subgraph"},
	}}
	return rt, scopes
}

func TestCarryExitWriteback_AcceptsValidJSON(t *testing.T) {
	t.Parallel()
	parentID := shared.UUID(uuid.New())
	exitID := shared.UUID(uuid.New())
	rt, scopes := makeFixture(parentID, exitID)

	writeback := json.RawMessage(`{"version_id":"v42","row_count":1024}`)
	if err := runtime.CarryExitWriteback(context.Background(),
		runtime.PropagationArgs{RunTree: rt, RunScopes: scopes}, nil, exitID, writeback); err != nil {
		t.Fatalf("CarryExitWriteback: %v", err)
	}
}

func TestCarryExitWriteback_RejectsNonJSONBytes(t *testing.T) {
	t.Parallel()
	parentID := shared.UUID(uuid.New())
	exitID := shared.UUID(uuid.New())
	rt, scopes := makeFixture(parentID, exitID)

	bogus := json.RawMessage(`not-json{`)
	err := runtime.CarryExitWriteback(context.Background(),
		runtime.PropagationArgs{RunTree: rt, RunScopes: scopes}, nil, exitID, bogus)
	if err == nil {
		t.Fatalf("expected JSON-decode error for non-JSON writeback bytes")
	}
}

func TestCarryExitWriteback_RejectsRunWithoutParent(t *testing.T) {
	t.Parallel()
	rootID := shared.UUID(uuid.New())
	rootScopeID := shared.UUID(uuid.New())
	root := &persistence.RunTreeRow{RunID: rootID, NodeID: shared.UUID(uuid.New()), RunScopeID: rootScopeID}
	rt := &fakeRunTreeForExit{rows: map[shared.UUID]*persistence.RunTreeRow{rootID: root}}
	scopes := &fakeRunScopeForExit{rows: map[shared.UUID]*persistence.RunScopeRow{
		rootScopeID: {ID: rootScopeID, GraphName: "main"}, // no parent
	}}

	wb := json.RawMessage(`{"a":1}`)
	err := runtime.CarryExitWriteback(context.Background(),
		runtime.PropagationArgs{RunTree: rt, RunScopes: scopes}, nil, rootID, wb)
	if err == nil {
		t.Fatalf("expected error for run without parent (root run cannot carry to a parent)")
	}
}

func TestCarryExitWriteback_NoOpOnEmptyWriteback(t *testing.T) {
	t.Parallel()
	parentID := shared.UUID(uuid.New())
	exitID := shared.UUID(uuid.New())
	rt, scopes := makeFixture(parentID, exitID)

	// Empty writeback is a legal no-op: per spec §Writeback carry-rule
	// for exit, "if exit never runs ... the parent's writeback row
	// remains empty." Zero-byte writeback is equivalent.
	if err := runtime.CarryExitWriteback(context.Background(),
		runtime.PropagationArgs{RunTree: rt, RunScopes: scopes}, nil, exitID, nil); err != nil {
		t.Errorf("CarryExitWriteback with empty writeback should be a no-op, got: %v", err)
	}
}
