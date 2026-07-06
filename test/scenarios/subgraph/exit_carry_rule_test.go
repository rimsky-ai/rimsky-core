// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

type carryFixture struct {
	tables          persistence.Tables
	instanceID      shared.UUID
	parentNodeRunID shared.UUID
	exitNodeRunID   shared.UUID
	exitScopeID     shared.UUID
	exitNodeID      shared.UUID
}

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

func makeFixture(t *testing.T) carryFixture {
	t.Helper()
	ctx := context.Background()
	tables := openSQLiteTables(t)

	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	callerNodeID := shared.UUID(uuid.New())
	exitNodeID := shared.UUID(uuid.New())
	parentNodeRunID := shared.UUID(uuid.New())
	exitScopeID := shared.UUID(uuid.New())
	exitNodeRunID := shared.UUID(uuid.New())

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
			ID:           instanceID,
			TemplateHash: templateHash,
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
		frameID, err := tables.Frames().InsertRunningFrame(ctx, instanceID, msgID, mainScopeID, 600000, tx)
		if err != nil {
			return err
		}
		if err := tables.NodeRunTree().CreateRootNodeRun(ctx, tx, persistence.CreateRootNodeRunInput{
			NodeRunID: parentNodeRunID, NodeID: callerNodeID, FrameID: frameID,
			RunScopeID: mainScopeID,
		}); err != nil {
			return err
		}
		parentRunIDCopy := parentNodeRunID
		mainScopeIDCopy := mainScopeID
		if err := tables.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:               exitScopeID,
			ParentRunScopeID: &mainScopeIDCopy,
			ParentNodeRunID:  &parentRunIDCopy,
			GraphName:        "inner",
			InstanceID:       instanceID,
		}); err != nil {
			return err
		}
		return tables.NodeRunTree().CreateChildNodeRun(ctx, tx, persistence.CreateChildNodeRunInput{
			NodeRunID: exitNodeRunID, NodeID: exitNodeID, FrameID: frameID,
			RunScopeID: exitScopeID, ExecutorName: "test-executor",
		})
	}); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}
	return carryFixture{
		tables:          tables,
		instanceID:      instanceID,
		parentNodeRunID: parentNodeRunID,
		exitNodeRunID:   exitNodeRunID,
		exitScopeID:     exitScopeID,
		exitNodeID:      exitNodeID,
	}
}

func settleCarry(fx carryFixture, exitNodeRunID shared.UUID, writeback json.RawMessage) error {
	args := runtime.RunArgs{Persist: fx.tables, Logger: shared.SilentLogger{}, Clock: shared.SystemClock{}}
	return fx.tables.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		return runtime.SettleFromDelegate(ctx, args, tx, runtime.DelegateSettlementInput{
			ExitNodeRunID: exitNodeRunID,
			ExitNodeID:    fx.exitNodeID,
			ExitNodeAlias: "inner-exit",
			InstanceID:    fx.instanceID,
			Writeback:     writeback,
		})
	})
}

func readParentAttrs(t *testing.T, fx carryFixture) *persistence.NodeAttributesRow {
	t.Helper()
	var attrs *persistence.NodeAttributesRow
	if err := fx.tables.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		var err error
		attrs, err = fx.tables.NodeAttributes().GetByRun(ctx, fx.parentNodeRunID, tx)
		return err
	}); err != nil {
		t.Fatalf("load parent attributes: %v", err)
	}
	return attrs
}

func TestSettleFromDelegate_CarryVerbatim_AcceptsValidJSON(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := makeFixture(t)

	writeback := json.RawMessage(`{"version_id":"v42","row_count":1024}`)
	if err := settleCarry(fx, fx.exitNodeRunID, writeback); err != nil {
		t.Fatalf("SettleFromDelegate (carry-verbatim): %v", err)
	}

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

func TestSettleFromDelegate_CarryVerbatim_RejectsNonJSONBytes(t *testing.T) {
	t.Parallel()
	fx := makeFixture(t)

	bogus := json.RawMessage(`not-json{`)
	if err := settleCarry(fx, fx.exitNodeRunID, bogus); err == nil {
		t.Fatalf("expected JSON-decode error for non-JSON writeback bytes")
	}
}

func TestSettleFromDelegate_CarryVerbatim_RejectsRunWithoutParent(t *testing.T) {
	t.Parallel()
	fx := makeFixture(t)

	wb := json.RawMessage(`{"a":1}`)
	if err := settleCarry(fx, fx.parentNodeRunID, wb); err == nil {
		t.Fatalf("expected error for run without parent (root run cannot carry to a parent)")
	}
}

func TestSettleFromDelegate_CarryVerbatim_EmptyWritebackSkipsOnlyAttributeCarry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fx := makeFixture(t)

	if err := settleCarry(fx, fx.exitNodeRunID, nil); err != nil {
		t.Errorf("SettleFromDelegate with empty writeback should succeed, got: %v", err)
	}
	attrs := readParentAttrs(t, fx)
	if attrs != nil && len(attrs.Data) > 0 {
		t.Errorf("parent writeback row should remain empty on empty carry, got %v", attrs.Data)
	}
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
