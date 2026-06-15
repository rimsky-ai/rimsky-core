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
// The carry is the carry-verbatim settlement shape of the unified
// settle-children primitive (`runtime.SettleChildren`): it consults the
// run-tree row + RunScope to locate the parent, validates the writeback
// bytes JSON-decode, upserts the parent's attribute row, and closes the
// sub-graph RunScope — all inside the caller's tx. Per
// @blessed-invariant 20 the primitive does not mangle bytes — it
// round-trips through json.Unmarshal only to enforce the schema
// contract. Exercised here against a real SQLite backend so the
// carry-writeback + scope-close atomicity is asserted against actual
// persisted rows.
package subgraph

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	persistence "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

// carryFixture is the persisted sub-graph shape the carry-rule fires
// against: a calling-node parent run in the instance's main RunScope,
// plus an exit leaf run inside a child RunScope whose parent_run_id
// points back at the parent run.
type carryFixture struct {
	tables      persistence.Tables
	instanceID  shared.UUID
	parentRunID shared.UUID
	exitRunID   shared.UUID
	exitScopeID shared.UUID
	exitNodeID  shared.UUID
}

// openSQLiteTables opens a throwaway file-backed SQLite database and
// migrates it.
func openSQLiteTables(t *testing.T) persistence.Tables {
	t.Helper()
	ctx := context.Background()
	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(t.TempDir(), "state.db")},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d.Tables()
}

// makeFixture seeds template → main RunScope → instance → nodes →
// frame → parent run → sub-graph RunScope → exit run.
func makeFixture(t *testing.T) carryFixture {
	t.Helper()
	ctx := context.Background()
	tables := openSQLiteTables(t)

	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	callerNodeID := shared.UUID(uuid.New())
	exitNodeID := shared.UUID(uuid.New())
	parentRunID := shared.UUID(uuid.New())
	exitScopeID := shared.UUID(uuid.New())
	exitRunID := shared.UUID(uuid.New())

	tmpl := tmplspec.TemplateSpec{
		Name:           "exit-carry-fixture",
		Version:        "1",
		FrameTimeoutMs: 600000,
		Nodes: []tmplspec.TemplateNodeDef{
			{Type: "caller", Delegate: "inner"},
			{Type: "inner-exit", Executor: "test-executor"},
		},
	}

	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := tables.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID:     templateHash,
			Spec:   tmpl,
			State:  persistence.TemplateStateRegistered,
			Source: "direct",
		}, tx); err != nil {
			return err
		}
		if err := tables.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:         mainScopeID,
			GraphName:  tmplspec.MainGraphName,
			InstanceID: instanceID,
		}); err != nil {
			return err
		}
		if _, err := tables.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID:             instanceID,
			TemplateHash:   templateHash,
			MainRunScopeID: mainScopeID,
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: callerNodeID, InstanceID: instanceID,
			NodeType: "caller",
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: exitNodeID, InstanceID: instanceID,
			NodeType: "inner-exit", Executor: "test-executor",
		}, tx); err != nil {
			return err
		}
		msgID := shared.UUID(uuid.New())
		if err := tables.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID:         msgID,
			InstanceID: instanceID,
			Type:       "test/seed",
			Sender:     "test",
			SenderKind: "operator",
		}); err != nil {
			return err
		}
		frameID, err := tables.Frames().InsertFrame(ctx, instanceID, msgID, 600000, tx)
		if err != nil {
			return err
		}
		if _, err := tables.Frames().PromoteQueuedFrameToRunning(ctx, frameID, tx); err != nil {
			return err
		}
		// @constraint: parent (calling-node) run in the main scope.
		if err := tables.RunTree().CreateRootRun(ctx, tx, persistence.CreateRootRunInput{
			RunID: parentRunID, NodeID: callerNodeID, FrameID: frameID,
			RunScopeID: mainScopeID,
		}); err != nil {
			return err
		}
		parentRunIDCopy := parentRunID
		mainScopeIDCopy := mainScopeID
		if err := tables.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:               exitScopeID,
			ParentRunScopeID: &mainScopeIDCopy,
			ParentRunID:      &parentRunIDCopy,
			GraphName:        "inner",
			InstanceID:       instanceID,
		}); err != nil {
			return err
		}
		return tables.RunTree().CreateChildRun(ctx, tx, persistence.CreateChildRunInput{
			RunID: exitRunID, NodeID: exitNodeID, FrameID: frameID,
			RunScopeID: exitScopeID, ExecutorName: "test-executor",
		})
	}); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}
	return carryFixture{
		tables:      tables,
		instanceID:  instanceID,
		parentRunID: parentRunID,
		exitRunID:   exitRunID,
		exitScopeID: exitScopeID,
		exitNodeID:  exitNodeID,
	}
}

// settleCarry drives runtime.SettleChildren with the carry-verbatim
// policy inside one transaction — the same shape the runner-tx wrapper
// (applyTerminalCompleteSubgraphExit) uses.
func settleCarry(fx carryFixture, exitRunID shared.UUID, writeback json.RawMessage) error {
	args := runtime.RunArgs{Persist: fx.tables, Logger: shared.SilentLogger{}}
	return fx.tables.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		return runtime.SettleChildren(ctx, args, tx, runtime.ChildSettlementInput{
			Policy:        tmplspec.AggregationPolicy{Kind: tmplspec.AggregationKindCarryVerbatim},
			ExitRunID:     exitRunID,
			ExitNodeID:    fx.exitNodeID,
			ExitNodeAlias: "inner-exit",
			InstanceID:    fx.instanceID,
			Writeback:     writeback,
		})
	})
}

// readParentAttrs loads the parent run's attribute row inside a tx
// (the sqlite driver enforces the no-nil-tx contract).
func readParentAttrs(t *testing.T, fx carryFixture) *persistence.NodeAttributesRow {
	t.Helper()
	var attrs *persistence.NodeAttributesRow
	if err := fx.tables.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		var err error
		attrs, err = fx.tables.NodeAttributes().GetByRun(ctx, fx.parentRunID, tx)
		return err
	}); err != nil {
		t.Fatalf("load parent attributes: %v", err)
	}
	return attrs
}

func TestSettleChildren_CarryVerbatim_AcceptsValidJSON(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := makeFixture(t)

	writeback := json.RawMessage(`{"version_id":"v42","row_count":1024}`)
	if err := settleCarry(fx, fx.exitRunID, writeback); err != nil {
		t.Fatalf("SettleChildren (carry-verbatim): %v", err)
	}

	// @deliberate: The carry landed verbatim on the PARENT run's attribute row
	// (@blessed-invariant: exit-node-writeback-to-parent — exit-node-writeback flows to parent run
	// writeback).
	attrs := readParentAttrs(t, fx)
	if attrs == nil {
		t.Fatalf("parent run has no attribute row after carry")
	}
	if got := attrs.Data["version_id"]; got != "v42" {
		t.Errorf("carried version_id = %v, want v42", got)
	}
	if got := attrs.Data["row_count"]; got != float64(1024) {
		t.Errorf("carried row_count = %v, want 1024", got)
	}

	// @deliberate: The child execution context closed atomically with the carry
	// (carry-rule atomicity: same transaction as the writeback).
	var scope *persistence.RunScopeRow
	if err := fx.tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		scope, err = fx.tables.RunScopes().GetByID(ctx, tx, fx.exitScopeID)
		return err
	}); err != nil {
		t.Fatalf("load exit scope: %v", err)
	}
	if scope == nil || scope.ClosedAt == nil {
		t.Errorf("sub-graph RunScope not closed after carry settlement")
	}

	instanceID := fx.instanceID
	var res persistence.EventListResult
	if err := fx.tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		res, err = fx.tables.Events().List(ctx,
			persistence.EventListFilter{InstanceID: &instanceID},
			persistence.ListPagination{Limit: 50}, tx)
		return err
	}); err != nil {
		t.Fatalf("list events: %v", err)
	}
	found := false
	for _, e := range res.Events {
		if e.Kind == events.KindSubgraphExitCarry() {
			found = true
		}
	}
	if !found {
		t.Errorf("no subgraph.exit_carry event after carry settlement")
	}
}

func TestSettleChildren_CarryVerbatim_RejectsNonJSONBytes(t *testing.T) {
	t.Parallel()
	fx := makeFixture(t)

	bogus := json.RawMessage(`not-json{`)
	if err := settleCarry(fx, fx.exitRunID, bogus); err == nil {
		t.Fatalf("expected JSON-decode error for non-JSON writeback bytes")
	}
}

func TestSettleChildren_CarryVerbatim_RejectsRunWithoutParent(t *testing.T) {
	t.Parallel()
	fx := makeFixture(t)

	// @constraint: The PARENT run lives in the main RunScope (no parent_run_id) —
	// settling it as if it were a sub-graph exit must error.
	wb := json.RawMessage(`{"a":1}`)
	if err := settleCarry(fx, fx.parentRunID, wb); err == nil {
		t.Fatalf("expected error for run without parent (root run cannot carry to a parent)")
	}
}

func TestSettleChildren_CarryVerbatim_EmptyWritebackSkipsOnlyAttributeCarry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := makeFixture(t)

	// @constraint: An empty writeback skips ONLY the attribute upsert: per spec
	// §Writeback carry-rule for exit, "if exit never runs ... the
	// parent's writeback row remains empty." Zero-byte writeback is
	// equivalent — but the REST of the settlement (RunScope close,
	// exit-carry forensics) must still run, or the scope leaks open.
	// The runner-wrapper twin of this case lives in
	// lib/runtime/subgraph_exit_carry_empty_test.go.
	if err := settleCarry(fx, fx.exitRunID, nil); err != nil {
		t.Errorf("SettleChildren with empty writeback should succeed, got: %v", err)
	}
	attrs := readParentAttrs(t, fx)
	if attrs != nil && len(attrs.Data) > 0 {
		t.Errorf("parent writeback row should remain empty on empty carry, got %v", attrs.Data)
	}
	// @deliberate: The sub-graph RunScope still closes on the empty carry.
	var scope *persistence.RunScopeRow
	if err := fx.tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		scope, err = fx.tables.RunScopes().GetByID(ctx, tx, fx.exitScopeID)
		return err
	}); err != nil {
		t.Fatalf("load exit scope: %v", err)
	}
	if scope == nil || scope.ClosedAt == nil {
		t.Errorf("sub-graph RunScope must close on the empty-writeback carry")
	}
}
