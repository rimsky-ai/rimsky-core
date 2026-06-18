// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	shared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	nodepkg "github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	pgtest "github.com/rimsky-ai/rimsky-core/test/support/pgmigrate"
)

func TestPullHardDepUpstreams_WakesParkedUpstream(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	spec := makeHardDepTemplate()
	tpl := insertDeployedTemplate(ctx, t, backend, spec)

	ck := "ck-" + uuid.NewString()
	var (
		inst persistence.InstanceRow
		aN   persistence.NodeRow
		bN   persistence.NodeRow
		cN   persistence.NodeRow
	)
	instID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:         mainScopeID,
			GraphName:  "main",
			InstanceID: instID,
		}); err != nil {
			return err
		}
		i, err := backend.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: instID, TemplateHash: tpl.ID, InstanceKey: &ck, Params: map[string]any{},
			MainRunScopeID: mainScopeID,
		}, tx)
		if err != nil {
			return err
		}
		inst = i
		for _, def := range spec.Nodes {
			n, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
				ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: def.Type, Executor: def.Executor,
			}, tx)
			if err != nil {
				return err
			}
			switch def.Type {
			case "a":
				aN = n
			case "b":
				bN = n
			case "c":
				cN = n
			}
		}
		return nil
	}))

	earlierFrameID := seedFrame(ctx, t, backend, inst.ID, bN.ID)
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := backend.Frames().MarkRunningFrameTerminal(ctx, earlierFrameID,
			persistence.FrameStateCompleted, tx)
		return err
	}))
	frameID := seedFrame(ctx, t, backend, inst.ID, aN.ID)

	aRunID := shared.UUID(uuid.New())
	pgtest.ExecForTest(ctx, t, d, `
        INSERT INTO rimsky_node_runs
            (id, node_id, executor_name, required_stores, enqueued_at, phase, state, frame_id, run_scope_id)
        VALUES ($1, $2, $3, ARRAY[]::text[], NOW(), 'active', 'running', $4, $5)
    `, aRunID, aN.ID, "stub", frameID, mainScopeID)
	pgtest.ExecForTest(ctx, t, d,
		`UPDATE rimsky_nodes SET frame_id = $1 WHERE id = $2`, frameID, aN.ID)

	bParkedRunID := shared.UUID(uuid.New())
	pgtest.ExecForTest(ctx, t, d, `
        INSERT INTO rimsky_node_runs
            (id, node_id, executor_name, required_stores, enqueued_at, phase, state, frame_id, parked_at, parked_reason, run_scope_id)
        VALUES ($1, $2, 'stub', ARRAY[]::text[], NOW(), 'parked', 'parked', $3, NOW(), 'await_callback', $4)
    `, bParkedRunID, bN.ID, earlierFrameID, mainScopeID)
	pgtest.ExecForTest(ctx, t, d,
		`UPDATE rimsky_nodes SET frame_id = $1 WHERE id = $2`, earlierFrameID, bN.ID)

	args := runtime.RunArgs{
		Persist: backend, Queue: d.Queue(), Clock: shared.SystemClock{}, Logger: shared.SilentLogger{},
	}
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return runtime.CascadeSubscribersStaleInTxForTest(
			ctx, args, tx, aN.ID, "a", aRunID, inst.ID, frameID,
		)
	}))

	var events persistence.EventListResult
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.Events().List(ctx, persistence.EventListFilter{NodeID: &bN.ID},
			persistence.ListPagination{Limit: 100}, tx)
		events = r
		return err
	}))
	wokeUp := false
	for _, e := range events.Events {
		if e.KindRaw == "parked_resume_started" {
			wokeUp = true
			break
		}
	}
	require.True(t, wokeUp,
		"pullForceRefreshUpstreams must wake b's parked run and emit parked_resume_started; events: %+v",
		events.Events)
	_ = cN
}

func TestPullHardDepUpstreams_NoExtraWakeForCurrentFrameInFlight(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	backend := d.Tables()

	spec := makeHardDepTemplate()
	tpl := insertDeployedTemplate(ctx, t, backend, spec)

	ck := "ck-" + uuid.NewString()
	var (
		inst persistence.InstanceRow
		aN   persistence.NodeRow
		bN   persistence.NodeRow
		cN   persistence.NodeRow
	)
	instID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:         mainScopeID,
			GraphName:  "main",
			InstanceID: instID,
		}); err != nil {
			return err
		}
		i, err := backend.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: instID, TemplateHash: tpl.ID, InstanceKey: &ck, Params: map[string]any{},
			MainRunScopeID: mainScopeID,
		}, tx)
		if err != nil {
			return err
		}
		inst = i
		for _, def := range spec.Nodes {
			n, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
				ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: def.Type, Executor: def.Executor,
			}, tx)
			if err != nil {
				return err
			}
			switch def.Type {
			case "a":
				aN = n
			case "b":
				bN = n
			case "c":
				cN = n
			}
		}
		return nil
	}))

	frameID := seedFrame(ctx, t, backend, inst.ID, aN.ID)

	aRunID := shared.UUID(uuid.New())
	pgtest.ExecForTest(ctx, t, d, `
        INSERT INTO rimsky_node_runs
            (id, node_id, executor_name, required_stores, enqueued_at, phase, state, frame_id, run_scope_id)
        VALUES ($1, $2, $3, ARRAY[]::text[], NOW(), 'active', 'running', $4, $5)
    `, aRunID, aN.ID, "stub", frameID, mainScopeID)
	pgtest.ExecForTest(ctx, t, d,
		`UPDATE rimsky_nodes SET frame_id = $1 WHERE id = $2`, frameID, aN.ID)

	bRunID := shared.UUID(uuid.New())
	pgtest.ExecForTest(ctx, t, d, `
        INSERT INTO rimsky_node_runs
            (id, node_id, executor_name, required_stores, enqueued_at, phase, state, frame_id, run_scope_id)
        VALUES ($1, $2, 'stub', ARRAY[]::text[], NOW(), 'pending', 'stale', $3, $4)
    `, bRunID, bN.ID, frameID, mainScopeID)
	pgtest.ExecForTest(ctx, t, d,
		`UPDATE rimsky_nodes SET frame_id = $1 WHERE id = $2`, frameID, bN.ID)

	args := runtime.RunArgs{
		Persist: backend, Queue: d.Queue(), Clock: shared.SystemClock{}, Logger: shared.SilentLogger{},
	}
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return runtime.CascadeSubscribersStaleInTxForTest(
			ctx, args, tx, aN.ID, "a", aRunID, inst.ID, frameID,
		)
	}))

	var events persistence.EventListResult
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.Events().List(ctx, persistence.EventListFilter{NodeID: &bN.ID},
			persistence.ListPagination{Limit: 100}, tx)
		events = r
		return err
	}))
	for _, e := range events.Events {
		require.NotEqualf(t, "parked_resume_started", e.Kind,
			"pullForceRefreshUpstreams must not fire wake on a non-parked in-flight upstream; events: %+v",
			events.Events)
	}
	_ = cN
}

func makeHardDepTemplate() nodepkg.TemplateSpec {
	mkSchema := func(field string) map[string]any {
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				field: map[string]any{"type": "string"},
			},
			"required": []any{field},
		}
	}
	cSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"a_val": map[string]any{
				"type":   "string",
				"source": "{{nodes.a.attribute.a_value}}",
			},
			"b_val": map[string]any{
				"type":   "string",
				"source": "{{nodes.b.attribute.b_value}}",
			},
		},
		"required": []any{"a_val", "b_val"},
	}
	return nodepkg.TemplateSpec{
		Name: "hard-dep-parked-" + uuid.NewString(), Version: "1",
		Nodes: []nodepkg.TemplateNodeDef{
			{Type: "a", Executor: "stub", Attributes: &nodepkg.NodeAttributesDef{Schema: mkSchema("a_value")}},
			{Type: "b", Executor: "stub", Attributes: &nodepkg.NodeAttributesDef{Schema: mkSchema("b_value")}},
			{
				Type: "c", Executor: "stub",
				Subscribes: []nodepkg.SubscriptionEntry{
					{Node: "a", Type: "terminal/*", WakeOnChange: nodepkg.BoolPtr(true), ForceUpstreamRefresh: nodepkg.BoolPtr(false)},
					{Node: "a", Type: "attribute/a_value/changed", WakeOnChange: nodepkg.BoolPtr(true), ForceUpstreamRefresh: nodepkg.BoolPtr(false)},
					{Node: "b", Type: "attribute/b_value/changed", WakeOnChange: nodepkg.BoolPtr(true), ForceUpstreamRefresh: nodepkg.BoolPtr(true)},
				},
				Attributes: &nodepkg.NodeAttributesDef{Schema: cSchema},
			},
		},
	}
}
