// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

func TestApplyTerminalCompleteSubgraphExit_EmptyAttributes_ClosesScopeAndEmitsCarry(t *testing.T) {
	t.Parallel()
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
	tables := d.Tables()

	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	callerNodeID := shared.UUID(uuid.New())
	exitNodeID := shared.UUID(uuid.New())
	parentRunID := shared.UUID(uuid.New())
	exitScopeID := shared.UUID(uuid.New())
	exitRunID := shared.UUID(uuid.New())

	tmpl := tmplspec.TemplateSpec{
		Name:           "exit-carry-empty-fixture",
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

	args := RunArgs{
		Persist:      tables,
		Queue:        d.Queue(),
		ClaimHandles: tables.ClaimHandles(),
		Logger:       shared.SilentLogger{},
		Clock:        shared.SystemClock{},
		SupervisorID: "sup-exit-carry-empty",
	}
	acq := &acquisition{
		DispatchID: exitRunID,
		NodeID:     exitNodeID,
		InstanceID: instanceID,
		NodeType:   "inner-exit",
		Executor:   "test-executor",
		GraphName:  "inner",
		RunScopeID: exitScopeID,
		NodeDef:    &node.TemplateNodeDef{Type: "inner-exit", Executor: "test-executor", IsSubgraphExit: true},
	}

	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return applyTerminalCompleteSubgraphExit(ctx, args, acq, map[string]any{}, tx)
	}); err != nil {
		t.Fatalf("applyTerminalCompleteSubgraphExit (empty attributes): %v", err)
	}

	var scope *persistence.RunScopeRow
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		scope, err = tables.RunScopes().GetByID(ctx, tx, exitScopeID)
		return err
	}); err != nil {
		t.Fatalf("load exit scope: %v", err)
	}
	if scope == nil || scope.ClosedAt == nil {
		t.Errorf("sub-graph RunScope not closed after empty-attribute exit settlement")
	}

	var res persistence.EventListResult
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		res, err = tables.Events().List(ctx,
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
		t.Errorf("no subgraph.exit_carry event after empty-attribute exit settlement")
	}

	var attrs *persistence.NodeAttributesRow
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		attrs, err = tables.NodeAttributes().GetByRun(ctx, parentRunID, tx)
		return err
	}); err != nil {
		t.Fatalf("load parent attributes: %v", err)
	}
	if attrs != nil && len(attrs.Data) > 0 {
		t.Errorf("parent writeback row should remain empty on empty-attribute carry, got %v", attrs.Data)
	}
}
