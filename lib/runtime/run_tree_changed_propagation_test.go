// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

// @concept: run-scope
func TestApplyTerminalComplete_LeafChangedPersistsAndPropagatesToRunTreeParent(t *testing.T) {
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
	q := d.Queue()

	templateHash := "sha256-" + uuid.NewString()
	instanceID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	childScopeID := shared.UUID(uuid.New())
	parentNodeID := shared.UUID(uuid.New())
	childNodeID := shared.UUID(uuid.New())
	var parentRunID, childRunID, frameID shared.UUID

	tmpl := spec.TemplateSpec{
		Name:    "changed-propagation-fixture",
		Version: "1",
		Nodes: []spec.TemplateNodeDef{
			{Type: "parent", Executor: ""},
			{Type: "child", Executor: "test-executor"},
		},
	}

	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := tables.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: templateHash, Spec: tmpl, State: persistence.TemplateStateRegistered, Source: "direct",
		}, tx); err != nil {
			return err
		}
		if err := tables.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID: mainScopeID, GraphName: spec.MainGraphName, InstanceID: instanceID,
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Instances().Create(ctx, persistence.InstanceCreateInput{TargetRoutingIdentity: "test-daemon",
			ID: instanceID, TemplateHash: templateHash,
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: parentNodeID, InstanceID: instanceID, NodeType: "parent", Executor: "",
		}, tx); err != nil {
			return err
		}
		if _, err := tables.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: childNodeID, InstanceID: instanceID, NodeType: "child", Executor: "test-executor",
		}, tx); err != nil {
			return err
		}
		msgID := shared.UUID(uuid.New())
		if err := tables.Messages().Insert(ctx, persistence.EnqueueMessageRequest{
			ID: msgID, InstanceID: instanceID, Type: "test/seed", Sender: "test", SenderKind: "operator",
		}, tx); err != nil {
			return err
		}
		fid, err := tables.Frames().InsertRunningFrame(ctx, instanceID, msgID, mainScopeID, tx)
		if err != nil {
			return err
		}
		frameID = fid

		parentRunID = shared.UUID(uuid.New())
		if err := tables.NodeRunTree().CreateRootNodeRun(ctx, persistence.CreateRootNodeRunInput{
			NodeRunID: parentRunID, NodeID: parentNodeID, FrameID: frameID, RunScopeID: mainScopeID,
			AggregationPolicy: spec.AggregationPolicy{Kind: spec.AggregationKindStrict},
		}, tx); err != nil {
			return err
		}
		if err := tables.Nodes().UpdateState(ctx, parentRunID, cascade.NodeStateRunning, cascade.ReasonDispatchClaimed, nil, tx); err != nil {
			return err
		}
		if err := tables.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID: childScopeID, ParentRunScopeID: &mainScopeID, ParentNodeRunID: &parentRunID,
			PartitionKey: "only", GraphName: "staging", InstanceID: instanceID,
		}, tx); err != nil {
			return err
		}
		if err := q.Enqueue(ctx, persistence.DispatchRequest{
			NodeID: childNodeID, ExecutorName: "test-executor", RequiredClaimProducers: []string{},
			EnqueuedAt: time.Now().Add(-time.Second), FrameID: frameID, RunScopeID: childScopeID,
		}, tx); err != nil {
			return err
		}
		cands, err := q.SelectCandidates(ctx, persistence.SelectCandidatesRequest{
			Limit: 16,
		}, tx)
		if err != nil {
			return err
		}
		for _, c := range cands {
			if c.NodeID == childNodeID {
				childRunID = c.NodeRunID
			}
		}
		if childRunID == (shared.UUID{}) {
			return fmt.Errorf("child candidate not surfaced")
		}
		claimed, err := q.ClaimDispatchRow(ctx, childRunID, "sup-changed-propagation", tx)
		if err != nil || !claimed {
			return fmt.Errorf("claim child: ok=%v err=%v", claimed, err)
		}
		promoted, err := q.PromoteClaimedToRunning(ctx, childRunID, "sup-changed-propagation", tx)
		if err != nil || !promoted {
			return fmt.Errorf("promote child: ok=%v err=%v", promoted, err)
		}
		return nil
	}))

	nodeDef := &node.TemplateNodeDef{Type: "child", Executor: "test-executor"}
	acq := &acquisition{
		NodeRunID: childRunID, NodeID: childNodeID, InstanceID: instanceID,
		NodeType: "child", Executor: "test-executor", GraphName: "staging",
		RunScopeID: childScopeID, FrameID: frameID, NodeDef: nodeDef,
	}
	args := RunArgs{
		Persist: tables, Queue: q, ClaimHandles: tables.ClaimHandles(),
		Logger: shared.SilentLogger{}, Clock: shared.SystemClock{}, SupervisorID: "sup-changed-propagation",
	}

	var post postCommitFn
	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		pc, err := applyTerminalComplete(ctx, args, acq, map[string]any{}, nil,
			terminalEvent{Kind: terminalKindComplete, Changed: true}, tx)
		post = pc
		return err
	}))
	if post != nil {
		post(ctx)
	}

	var childRow *persistence.NodeRunTreeRow
	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := tables.NodeRunTree().GetByID(ctx, childRunID, tx)
		childRow = r
		return err
	}))
	require.NotNil(t, childRow)
	require.True(t, childRow.Changed,
		"the leaf's own run-tree row must persist changed=true from the terminal event")

	var parentRow *persistence.NodeRunTreeRow
	require.NoError(t, tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := tables.NodeRunTree().GetByID(ctx, parentRunID, tx)
		parentRow = r
		return err
	}))
	require.NotNil(t, parentRow)
	require.Equal(t, cascade.NodeStateFresh, parentRow.State)
	require.True(t, parentRow.Changed,
		"run-tree aggregation must read the leaf's actual persisted changed value (not a hardcoded "+
			"true) and the strict-policy parent's only child settled changed=true, so the parent's own "+
			"settlement must report changed=true too")
}
