// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
	parentNodeRunID := shared.UUID(uuid.New())
	exitScopeID := shared.UUID(uuid.New())
	exitNodeRunID := shared.UUID(uuid.New())

	tmpl := tmplspec.TemplateSpec{
		Name:    "exit-carry-empty-fixture",
		Version: "1",
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
		if err := tables.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID:         mainScopeID,
			GraphName:  tmplspec.MainGraphName,
			InstanceID: instanceID,
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Instances().Create(ctx, persistence.InstanceCreateInput{TargetRoutingIdentity: "test-daemon",
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
		if err := tables.Messages().Insert(ctx, persistence.EnqueueMessageRequest{
			ID:         msgID,
			InstanceID: instanceID,
			Type:       "test/seed",
			Sender:     "test",
			SenderKind: "operator",
		}, tx); err != nil {
			return err
		}
		frameID, err := tables.Frames().InsertRunningFrame(ctx, instanceID, msgID, mainScopeID, tx)
		if err != nil {
			return err
		}
		if err := tables.NodeRunTree().CreateRootNodeRun(ctx, persistence.CreateRootNodeRunInput{
			NodeRunID: parentNodeRunID, NodeID: callerNodeID, FrameID: frameID,
			RunScopeID: mainScopeID,
		}, tx); err != nil {
			return err
		}
		parentRunIDCopy := parentNodeRunID
		mainScopeIDCopy := mainScopeID
		if err := tables.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID:               exitScopeID,
			ParentRunScopeID: &mainScopeIDCopy,
			ParentNodeRunID:  &parentRunIDCopy,
			GraphName:        "inner",
			InstanceID:       instanceID,
		}, tx); err != nil {
			return err
		}
		return tables.NodeRunTree().CreateChildNodeRun(ctx, persistence.CreateChildNodeRunInput{
			NodeRunID: exitNodeRunID, NodeID: exitNodeID, FrameID: frameID,
			RunScopeID: exitScopeID, ExecutorName: "test-executor",
		}, tx)
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
		NodeRunID:  exitNodeRunID,
		NodeID:     exitNodeID,
		InstanceID: instanceID,
		NodeType:   "inner-exit",
		Executor:   "test-executor",
		GraphName:  "inner",
		RunScopeID: exitScopeID,
		NodeDef:    &node.TemplateNodeDef{Type: "inner-exit", Executor: "test-executor", IsSubgraphExit: true},
	}

	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := applyTerminalCompleteSubgraphExit(ctx, args, acq, map[string]any{}, tx)
		return err
	}); err != nil {
		t.Fatalf("applyTerminalCompleteSubgraphExit (empty attributes): %v", err)
	}

	var scope *persistence.RunScopeRow
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		scope, err = tables.RunScopes().GetByID(ctx, exitScopeID, tx)
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
		attrs, err = tables.NodeAttributes().GetByRun(ctx, parentNodeRunID, tx)
		return err
	}); err != nil {
		t.Fatalf("load parent attributes: %v", err)
	}
	if attrs != nil && len(attrs.Data) > 0 {
		t.Errorf("parent writeback row should remain empty on empty-attribute carry, got %v", attrs.Data)
	}
}
