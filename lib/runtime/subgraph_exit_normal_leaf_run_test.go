// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

func TestApplyTerminalComplete_SubgraphExit_EmitsNormalLeafRunRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(t.TempDir(), "state.db")},
	})
	require.NoError(t, err)
	require.NoError(t, d.Migrate(ctx, shared.SilentLogger{}))
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
		Name:           "exit-normal-leaf-run-fixture",
		Version:        "1",
		FrameTimeoutMs: 600000,
		Nodes: []tmplspec.TemplateNodeDef{
			{Type: "caller", Delegate: "inner"},
			{Type: "inner-exit", Executor: "test-executor"},
		},
	}

	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := tables.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: templateHash, Spec: tmpl, State: persistence.TemplateStateRegistered, Source: "direct",
		}, tx); err != nil {
			return err
		}
		if err := tables.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID: mainScopeID, GraphName: tmplspec.MainGraphName, InstanceID: instanceID,
		}); err != nil {
			return err
		}
		if _, err := tables.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: instanceID, TemplateHash: templateHash,
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: callerNodeID, InstanceID: instanceID, NodeType: "caller",
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: exitNodeID, InstanceID: instanceID, NodeType: "inner-exit", Executor: "test-executor",
		}, tx); err != nil {
			return err
		}
		msgID := shared.UUID(uuid.New())
		if err := tables.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID: msgID, InstanceID: instanceID, Type: "test/seed", Sender: "test", SenderKind: "operator",
		}); err != nil {
			return err
		}
		frameID, err := tables.Frames().InsertRunningFrame(ctx, instanceID, msgID, mainScopeID, 600000, tx)
		if err != nil {
			return err
		}
		if err := tables.NodeRunTree().CreateRootNodeRun(ctx, tx, persistence.CreateRootNodeRunInput{
			NodeRunID: parentNodeRunID, NodeID: callerNodeID, FrameID: frameID, RunScopeID: mainScopeID,
		}); err != nil {
			return err
		}
		parentRunIDCopy := parentNodeRunID
		mainScopeIDCopy := mainScopeID
		if err := tables.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID: exitScopeID, ParentRunScopeID: &mainScopeIDCopy, ParentNodeRunID: &parentRunIDCopy,
			GraphName: "inner", InstanceID: instanceID,
		}); err != nil {
			return err
		}
		if err := tables.NodeRunTree().CreateChildNodeRun(ctx, tx, persistence.CreateChildNodeRunInput{
			NodeRunID: exitNodeRunID, NodeID: exitNodeID, FrameID: frameID,
			RunScopeID: exitScopeID, ExecutorName: "test-executor",
		}); err != nil {
			return err
		}
		return tables.NodeRunTree().UpdateStateAndOutcome(ctx, tx, exitNodeRunID, cascade.NodeStateRunning, nil)
	}))

	args := RunArgs{
		Persist:      tables,
		Queue:        d.Queue(),
		ClaimHandles: tables.ClaimHandles(),
		Logger:       shared.SilentLogger{},
		Clock:        shared.SystemClock{},
		SupervisorID: "sup-exit-normal-leaf-run",
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

	var post postCommitFn
	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		pc, err := applyTerminalComplete(ctx, args, acq, map[string]any{}, nil,
			terminalEvent{Kind: terminalKindComplete, Changed: true}, tx)
		if err != nil {
			return err
		}
		post = pc
		return nil
	}))
	require.NotNil(t, post, "an exit node must produce a post-commit hook exactly like any other executor-bearing node")
	post(ctx)

	rows, err := tables.Lineage().GetByRunID(ctx, exitNodeRunID)
	require.NoError(t, err)
	require.Len(t, rows, 1,
		"a subgraph exit node must emit exactly one leaf_run lineage row for its own run — "+
			"exit settlement (the writeback to the caller) is a side effect alongside the exit "+
			"node's own normal terminal lineage, not a replacement for it")

	row := rows[0]
	require.Equal(t, persistence.LineageRecordKindLeafRun, row.RecordKind)

	var rec LeafRunRecord
	require.NoError(t, json.Unmarshal(row.Record, &rec))
	require.Equal(t, "complete", rec.TerminalKind,
		"an exit node's own leaf_run row must carry terminal_kind=complete, the same discriminator "+
			"as any ordinary executor-bearing node — it must NOT be tagged subgraph_call (that's "+
			"reserved for the delegating CALLER's pass-through row) or omitted as though the exit "+
			"node itself were pure pass-through")
	require.Equal(t, string(cascade.NodeStateFresh), rec.State)
	require.Equal(t, "inner-exit", rec.NodeAlias)
	require.Equal(t, "test-executor", rec.ExecutorName,
		"the exit node's executor name must be recorded — proof it dispatched as a real "+
			"executor-bearing node rather than being treated as pass-through")
}
