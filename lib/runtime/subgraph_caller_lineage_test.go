// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// subgraph_caller_lineage_test.go — pins the `terminal_kind:
// "subgraph_call"` lineage emission shape against a real Postgres.
// Drives `applyTerminalCompleteSubgraphCaller` with a minimal
// acquisition fixture (template + instance + nodes + run rows) and
// asserts the row lands in `rimsky_lineage` with the expected
// discriminator + hashed inputs + parent_run_id.
//
// Lives in `package runtime` so it can construct the unexported
// `acquisition` + `terminalEvent` values the function consumes.

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

	// @deliberate: Seed a sub-graph template: main graph has one calling node
	// `outer-caller` delegating to graph `staging`; staging has entry
	// `validate` (absorbed), interior `transform`, exit `promote`.
	tmplSpec := node.TemplateSpec{
		Name:                "subgraph-lineage-emit",
		Version:             "1",
		FrameResolutionMode: node.FrameResolutionCoalesce,
		Graphs: []spec.GraphSpec{
			{
				Name: spec.MainGraphName,
				Nodes: []node.TemplateNodeDef{
					// @deliberate: delegate + executor are mutually exclusive; caller
					// has no executor. The runner treats sub-graph
					// callers as `Executor == ""` and goes through the
					// native dispatch path until the absorbed entry
					// terminal fires.
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
						Subscribes: []spec.SubscriptionEntry{{Node: "validate", Type: "terminal/*"}}},
					{Type: "promote", Executor: "stub",
						Subscribes: []spec.SubscriptionEntry{{Node: "transform", Type: "terminal/*"}}},
				},
			},
		},
	}
	// @deliberate: Canonicalize subscription edges for sub-graph (sets
	// ResolvesViaCallingNode on the transform→validate edge).
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
		// @deliberate: Create rimsky_nodes rows for every template node. The caller's
		// row is the test's subject (referenced by acquisition); the
		// staging nodes (validate/transform/promote) must exist in the
		// table because `applyTerminalCompleteSubgraphCaller` walks
		// `Nodes().ListByInstance` to look up the internal-cascade
		// dispatch targets by node_type — if any staging row is missing
		// the function returns a "no rimsky_nodes row" error before
		// reaching the lineage emission this test pins.
		mk := func(nodeType, executor string) (persistence.NodeRow, error) {
			return backend.Nodes().Create(ctx, persistence.NodeCreateInput{
				ID: shared.UUID(uuid.New()), InstanceID: inst.ID,
				NodeType: nodeType, Executor: executor,
			}, tx)
		}
		// @deliberate: Caller carries delegate, not executor; the test only asserts
		// on this row's lineage emission.
		if callerNode, err = mk("outer-caller", ""); err != nil {
			return err
		}
		// @deliberate: Staging nodes — created so the internal-cascade lookup
		// succeeds, then dropped on the floor (no further references).
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

	// @deliberate: Frame + caller's run row.
	var (
		frameID     shared.UUID
		callerRunID shared.UUID
	)
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		fid, err := backend.Frames().EnqueueSerialFrame(ctx, inst.ID, callerNode.ID, 600000, tx)
		if err != nil {
			return err
		}
		if _, err := backend.Frames().PromoteQueuedFrameToRunning(ctx, fid, tx); err != nil {
			return err
		}
		frameID = fid
		// @deliberate: Create a root run row for the caller (parent_run_id = nil so
		// the lineage row's parent_run_id key drops via omitempty).
		callerRunID = shared.UUID(uuid.New())
		if err := backend.RunTree().CreateRootRun(ctx, tx, persistence.CreateRootRunInput{
			RunID:      callerRunID,
			NodeID:     callerNode.ID,
			FrameID:    frameID,
			RunScopeID: inst.MainRunScopeID,
			// @deliberate: ExecutorName empty: sub-graph callers carry delegate, not
			// executor (the two are mutually exclusive at the template
			// layer).
			ExecutorName: "",
		}); err != nil {
			return err
		}
		// @deliberate: Move the run to running so the state-machine self-transition
		// running → running under the subgraph_internal_cascade_fired
		// reason is legal.
		return backend.RunTree().UpdateStateAndOutcome(ctx, tx, callerRunID,
			cascade.NodeStateRunning, nil)
	}))

	// @deliberate: Build the acquisition matching the caller node. lookupNodeDef
	// walks the canonicalized template (which copies graph-scoped nodes
	// onto TemplateSpec.Nodes at validation time).
	nodeDef := lookupNodeDef(&tmplSpec, "outer-caller")
	require.NotNil(t, nodeDef, "outer-caller node def must exist in main graph")
	require.Equal(t, "staging", nodeDef.Delegate, "node def must carry the delegate target")

	acq := &acquisition{
		DispatchID: callerRunID,
		NodeID:     callerNode.ID,
		InstanceID: inst.ID,
		NodeType:   "outer-caller",
		// @deliberate: Sub-graph callers have no executor (delegate + executor are
		// mutually exclusive at the template layer); leave empty.
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

	// @deliberate: Drive the sub-graph caller terminal. The merged map is what the
	// caller would normally pass through to upsertFinalAttributesTx as
	// the absorbed entry's attribute writeback; an empty map is fine
	// for the lineage assertion since the row encodes hashed inputs,
	// not raw bytes. Runs in the same outer-tx shape the runtime uses
	// — applyTerminalCompleteSubgraphCaller now expects to participate
	// in the determinism tx and returns a postCommit closure for the
	// lineage emit + observability.
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

	// @deliberate: Inspect the lineage projection for the caller's run.
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
	// @deliberate: Sub-graph callers have no executor; the lineage row's
	// `executor_name` is the empty string (dropped by omitempty when
	// the row is re-marshalled).
	require.Empty(t, rec.ExecutorName)
	require.True(t, rec.Changed, "changed=true threaded through from terminalEvent")
	require.NotEmpty(t, rec.ParamsSnapshotHash, "params snapshot hash must be populated from acq.InstanceParams")
	require.NotEmpty(t, rec.AttributesHash, "attributes hash must be populated from acq.MergedAttributes")
	require.Equal(t, tmplHash, rec.TemplateHash, "template_hash must be threaded through from acq.TemplateHash")
	// @deliberate: Root caller: ParentRunID must be empty + the JSON key dropped.
	require.Empty(t, rec.ParentRunID)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(row.Record, &raw))
	_, hasParent := raw["parent_run_id"]
	require.False(t, hasParent, "root caller must omit parent_run_id from the JSON payload")

	// @deliberate: Wait a few microseconds and re-check that no spurious second row
	// landed (the second `complete` row is emitted later by
	// applyTerminalComplete; this test only drives the first emission).
	time.Sleep(10 * time.Millisecond)
	rows2, err := backend.Lineage().GetByRunID(ctx, callerRunID)
	require.NoError(t, err)
	require.Len(t, rows2, 1, "applyTerminalCompleteSubgraphCaller must produce exactly one row per call")
}
