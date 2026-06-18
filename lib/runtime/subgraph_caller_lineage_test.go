// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	pgtest "github.com/rimsky-ai/rimsky-core/test/support/pgmigrate"
)

func TestSubgraphCallerLineage_EmitsSubgraphCallRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	tmplSpec := node.TemplateSpec{
		Name:    "subgraph-lineage-emit",
		Version: "1",
		Graphs: []spec.GraphSpec{
			{
				Name: spec.MainGraphName,
				Nodes: []node.TemplateNodeDef{
					{Type: "outer-caller", Delegate: "staging"},
				},
			},
			{
				Name:  "staging",
				Entry: "validate",
				Exit:  "promote",
				Nodes: []node.TemplateNodeDef{
					{Type: "validate", Executor: "stub"},
					{Type: "transform", Executor: "stub",
						Subscribes: []spec.SubscriptionEntry{{Node: "validate", Type: "terminal/*", WakeOnChange: spec.BoolPtr(true), ForceUpstreamRefresh: spec.BoolPtr(false)}}},
					{Type: "promote", Executor: "stub",
						Subscribes: []spec.SubscriptionEntry{{Node: "transform", Type: "terminal/*", WakeOnChange: spec.BoolPtr(true), ForceUpstreamRefresh: spec.BoolPtr(false)}}},
				},
			},
		},
	}
	if res := node.ValidateTemplate(&tmplSpec, node.RegistryHooks{}); len(res.Errors) != 0 {
		t.Fatalf("validate template: %v", res.Errors)
	}
	sum := sha256.Sum256([]byte(tmplSpec.Name + ":" + tmplSpec.Version))
	tmplHash := "sha256-" + hex.EncodeToString(sum[:])

	var (
		inst       persistence.InstanceRow
		callerNode persistence.NodeRow
	)
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: tmplHash, Spec: tmplSpec, State: persistence.TemplateStateRegistered,
		}, tx); err != nil {
			return err
		}
		if err := backend.Templates().UpdateState(ctx, tmplHash, persistence.TemplateStateDeployed, tx); err != nil {
			return err
		}
		ck := "ck-subgraph-emit"
		instID := shared.UUID(uuid.New())
		mainScopeID := shared.UUID(uuid.New())
		if err := backend.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:         mainScopeID,
			GraphName:  "main",
			InstanceID: instID,
		}); err != nil {
			return err
		}
		i, err := backend.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: instID, TemplateHash: tmplHash, InstanceKey: &ck,
			Params:         map[string]any{"region": "us-east"},
			MainRunScopeID: mainScopeID,
		}, tx)
		if err != nil {
			return err
		}
		inst = i
		mk := func(nodeType, executor string) (persistence.NodeRow, error) {
			return backend.Nodes().Create(ctx, persistence.NodeCreateInput{
				ID: shared.UUID(uuid.New()), InstanceID: inst.ID,
				NodeType: nodeType, Executor: executor,
			}, tx)
		}
		if callerNode, err = mk("outer-caller", ""); err != nil {
			return err
		}
		if _, err = mk("validate", "stub"); err != nil {
			return err
		}
		if _, err = mk("transform", "stub"); err != nil {
			return err
		}
		if _, err = mk("promote", "stub"); err != nil {
			return err
		}
		return nil
	}))

	var (
		frameID     shared.UUID
		callerRunID shared.UUID
	)
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		msgID := shared.UUID(uuid.New())
		if err := backend.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID:         msgID,
			InstanceID: inst.ID,
			Type:       "test/seed",
			Sender:     "test",
			SenderKind: "operator",
		}); err != nil {
			return err
		}
		fid, err := backend.Frames().InsertFrame(ctx, inst.ID, msgID, 600000, tx)
		if err != nil {
			return err
		}
		if _, err := backend.Frames().PromoteQueuedFrameToRunning(ctx, fid, tx); err != nil {
			return err
		}
		frameID = fid
		callerRunID = shared.UUID(uuid.New())
		if err := backend.RunTree().CreateRootRun(ctx, tx, persistence.CreateRootRunInput{
			RunID:        callerRunID,
			NodeID:       callerNode.ID,
			FrameID:      frameID,
			RunScopeID:   inst.MainRunScopeID,
			ExecutorName: "",
		}); err != nil {
			return err
		}
		return backend.RunTree().UpdateStateAndOutcome(ctx, tx, callerRunID,
			cascade.NodeStateRunning, nil)
	}))

	nodeDef := lookupNodeDef(&tmplSpec, "outer-caller")
	require.NotNil(t, nodeDef, "outer-caller node def must exist in main graph")
	require.Equal(t, "staging", nodeDef.Delegate, "node def must carry the delegate target")

	acq := &acquisition{
		DispatchID:       callerRunID,
		NodeID:           callerNode.ID,
		InstanceID:       inst.ID,
		NodeType:         "outer-caller",
		Executor:         "",
		GraphName:        "main",
		FrameID:          frameID,
		RunScopeID:       inst.MainRunScopeID,
		NodeDef:          nodeDef,
		InstanceParams:   inst.Params,
		MergedAttributes: map[string]any{"merged": true},
		TemplateHash:     tmplHash,
	}

	args := RunArgs{
		Persist:      backend,
		Queue:        d.Queue(),
		ClaimHandles: backend.ClaimHandles(),
		Logger:       shared.SilentLogger{},
		Clock:        shared.SystemClock{},
		SupervisorID: "sup-subgraph-lineage",
	}

	var post postCommitFn
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		pc, err := applyTerminalCompleteSubgraphCaller(
			ctx, args, acq, map[string]any{}, terminalEvent{
				Kind:    terminalKindComplete,
				Changed: true,
			}, tx)
		if err != nil {
			return err
		}
		post = pc
		return nil
	}))
	if post != nil {
		post(ctx)
	}

	rows, err := backend.Lineage().GetByRunID(ctx, callerRunID)
	require.NoError(t, err)
	require.Len(t, rows, 1, "applyTerminalCompleteSubgraphCaller must emit exactly one leaf_run row for the caller's run")
	row := rows[0]
	require.Equal(t, persistence.LineageRecordKindLeafRun, row.RecordKind)
	require.Equal(t, inst.ID, row.InstanceID)
	require.Equal(t, frameID, row.FrameID)

	var rec LeafRunRecord
	require.NoError(t, json.Unmarshal(row.Record, &rec))
	require.Equal(t, "subgraph_call", rec.TerminalKind,
		"the row must discriminate as terminal_kind=subgraph_call")
	require.Equal(t, string(cascade.NodeStateRunning), rec.State,
		"state must be `running` (parent stays running across the internal cascade)")
	require.Equal(t, callerRunID, rec.RunID)
	require.Equal(t, "outer-caller", rec.NodeAlias)
	require.Empty(t, rec.ExecutorName)
	require.True(t, rec.Changed, "changed=true threaded through from terminalEvent")
	require.NotEmpty(t, rec.ParamsSnapshotHash, "params snapshot hash must be populated from acq.InstanceParams")
	require.NotEmpty(t, rec.AttributesHash, "attributes hash must be populated from acq.MergedAttributes")
	require.Equal(t, tmplHash, rec.TemplateHash, "template_hash must be threaded through from acq.TemplateHash")
	require.Empty(t, rec.ParentRunID)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(row.Record, &raw))
	_, hasParent := raw["parent_run_id"]
	require.False(t, hasParent, "root caller must omit parent_run_id from the JSON payload")

	time.Sleep(10 * time.Millisecond)
	rows2, err := backend.Lineage().GetByRunID(ctx, callerRunID)
	require.NoError(t, err)
	require.Len(t, rows2, 1, "applyTerminalCompleteSubgraphCaller must produce exactly one row per call")
}
