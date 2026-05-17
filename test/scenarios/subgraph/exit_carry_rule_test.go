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
// it consults the run-tree row to locate the parent and validates the
// writeback bytes JSON-decode. Per @blessed-invariant 20 the helper
// does not mangle bytes — it round-trips through json.Unmarshal only
// to enforce the schema contract.
package subgraph

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/cascade"
	persistence "github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	tmplspec "github.com/fallguy/rimsky/foundation/spec"
	"github.com/fallguy/rimsky/runtime"
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
func (f *fakeRunTreeForExit) GetByParentChildKey(ctx context.Context, tx persistence.Tx, parentRunID shared.UUID, childKey string) (*persistence.RunTreeRow, error) {
	return nil, nil
}
func (f *fakeRunTreeForExit) LockTreeForUpdate(ctx context.Context, tx persistence.Tx, runID shared.UUID) (*persistence.RunTreeRow, error) {
	return f.rows[runID], nil
}
func (f *fakeRunTreeForExit) ListChildren(ctx context.Context, tx persistence.Tx, parentRunID shared.UUID) ([]persistence.RunTreeRow, error) {
	out := []persistence.RunTreeRow{}
	for _, r := range f.rows {
		if r.ParentRunID != nil && *r.ParentRunID == parentRunID {
			out = append(out, *r)
		}
	}
	return out, nil
}
func (f *fakeRunTreeForExit) UpdateStateAndOutcome(ctx context.Context, tx persistence.Tx, runID shared.UUID, state cascade.NodeState, lastOutcome cascade.LastOutcome) error {
	return nil
}
func (f *fakeRunTreeForExit) UpdateAggregationPolicy(ctx context.Context, tx persistence.Tx, runID shared.UUID, policy tmplspec.AggregationPolicy) error {
	return nil
}

func TestCarryExitWriteback_AcceptsValidJSON(t *testing.T) {
	t.Parallel()
	parentID := shared.UUID(uuid.New())
	exitID := shared.UUID(uuid.New())
	parent := &persistence.RunTreeRow{RunID: parentID, NodeID: shared.UUID(uuid.New()), State: cascade.NodeStateRunning}
	exit := &persistence.RunTreeRow{RunID: exitID, ParentRunID: &parentID, NodeID: shared.UUID(uuid.New()), State: cascade.NodeStateRunning}
	rt := &fakeRunTreeForExit{rows: map[shared.UUID]*persistence.RunTreeRow{parentID: parent, exitID: exit}}

	writeback := json.RawMessage(`{"version_id":"v42","row_count":1024}`)
	if err := runtime.CarryExitWriteback(context.Background(),
		runtime.PropagationArgs{RunTree: rt}, nil, exitID, writeback); err != nil {
		t.Fatalf("CarryExitWriteback: %v", err)
	}
}

func TestCarryExitWriteback_RejectsNonJSONBytes(t *testing.T) {
	t.Parallel()
	parentID := shared.UUID(uuid.New())
	exitID := shared.UUID(uuid.New())
	parent := &persistence.RunTreeRow{RunID: parentID, NodeID: shared.UUID(uuid.New())}
	exit := &persistence.RunTreeRow{RunID: exitID, ParentRunID: &parentID, NodeID: shared.UUID(uuid.New())}
	rt := &fakeRunTreeForExit{rows: map[shared.UUID]*persistence.RunTreeRow{parentID: parent, exitID: exit}}

	bogus := json.RawMessage(`not-json{`)
	err := runtime.CarryExitWriteback(context.Background(),
		runtime.PropagationArgs{RunTree: rt}, nil, exitID, bogus)
	if err == nil {
		t.Fatalf("expected JSON-decode error for non-JSON writeback bytes")
	}
}

func TestCarryExitWriteback_RejectsRunWithoutParent(t *testing.T) {
	t.Parallel()
	rootID := shared.UUID(uuid.New())
	root := &persistence.RunTreeRow{RunID: rootID, NodeID: shared.UUID(uuid.New())}
	rt := &fakeRunTreeForExit{rows: map[shared.UUID]*persistence.RunTreeRow{rootID: root}}

	wb := json.RawMessage(`{"a":1}`)
	err := runtime.CarryExitWriteback(context.Background(),
		runtime.PropagationArgs{RunTree: rt}, nil, rootID, wb)
	if err == nil {
		t.Fatalf("expected error for run without parent (root run cannot carry to a parent)")
	}
}

func TestCarryExitWriteback_NoOpOnEmptyWriteback(t *testing.T) {
	t.Parallel()
	parentID := shared.UUID(uuid.New())
	exitID := shared.UUID(uuid.New())
	parent := &persistence.RunTreeRow{RunID: parentID, NodeID: shared.UUID(uuid.New())}
	exit := &persistence.RunTreeRow{RunID: exitID, ParentRunID: &parentID, NodeID: shared.UUID(uuid.New())}
	rt := &fakeRunTreeForExit{rows: map[shared.UUID]*persistence.RunTreeRow{parentID: parent, exitID: exit}}

	// Empty writeback is a legal no-op: per spec §Writeback carry-rule
	// for exit, "if exit never runs ... the parent's writeback row
	// remains empty." Zero-byte writeback is equivalent.
	if err := runtime.CarryExitWriteback(context.Background(),
		runtime.PropagationArgs{RunTree: rt}, nil, exitID, nil); err != nil {
		t.Errorf("CarryExitWriteback with empty writeback should be a no-op, got: %v", err)
	}
}
