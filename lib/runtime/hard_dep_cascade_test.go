// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Unit tests for the upstream-refresh cascade extension's parked-
// upstream branch in pullForceRefreshUpstreams. Exercises the wake
// primitive in isolation: a parked upstream + an in-frame cascade walk
// to a receiver that declares `force_upstream_refresh: true` on that
// upstream must route through wakeParkedReceiverInTx and emit
// parked_resume_started.

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

// TestPullHardDepUpstreams_WakesParkedUpstream verifies the
// parked-upstream branch of pullForceRefreshUpstreams when the upstream
// is parked IN AN EARLIER FRAME (case 2). Setup:
//
//   - Template: a (sender), b (parked upstream), c (receiver). c
//     subscribes to a (state) and carries a `force_upstream_refresh:
//     true` subscription on b's attribute.
//   - F1 is the running frame. a has an in-flight 'active' run id
//     in F1. b has a parked node-run row pinned to an earlier frame
//     F0. c has no in-flight row yet.
//   - Invoke `runtime.CascadeSubscribersStaleInTxForTest` for sender=a.
//     The BFS visits c (subscriber), MarkStaleForCascade(c) inserts
//     c's pending run, then pullForceRefreshUpstreams(c) runs.
//     `GetInFlightRunForNode(b, F1)` returns hasRun=false (b's parked
//     row is in F0, not F1). The parked-branch probe fires:
//     GetParkedByNode(b) → parked, wakeParkedReceiverInTx wakes b
//     and rebinds the run to F1.
//
// Asserts: parked_resume_started event is emitted for b after the
// cascade walk.
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

	// @deliberate: Seed an EARLIER frame F0 where b parked, then a NEW running frame
	// F1 where the cascade walk fires. b's parked-frame is F0 so
	// GetInFlightRunForNode(b, F1) returns hasRun=false and the
	// parked-branch probe + wake fires.
	//
	// Mark F0 completed before opening F1 — the uq_rimsky_frames_running
	// constraint admits at most one running frame per instance.
	earlierFrameID := seedFrame(ctx, t, backend, inst.ID, bN.ID)
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := backend.Frames().MarkRunningFrameTerminal(ctx, earlierFrameID,
			persistence.FrameStateCompleted, tx)
		return err
	}))
	frameID := seedFrame(ctx, t, backend, inst.ID, aN.ID)

	// @deliberate: Seed sender (a) with an in-flight 'active' run in the running
	// frame F1 so the cascade walk can resolve it as the sender.
	aRunID := shared.UUID(uuid.New())
	pgtest.ExecForTest(ctx, t, d, `
        INSERT INTO rimsky_node_runs
            (id, node_id, executor_name, required_stores, enqueued_at, phase, state, frame_id, run_scope_id)
        VALUES ($1, $2, $3, ARRAY[]::text[], NOW(), 'active', 'running', $4, $5)
    `, aRunID, aN.ID, "stub", frameID, mainScopeID)
	pgtest.ExecForTest(ctx, t, d,
		`UPDATE rimsky_nodes SET frame_id = $1 WHERE id = $2`, frameID, aN.ID)

	// @deliberate: Seed b with a parked node-run row in F0 (an earlier frame).
	// State=parked, phase=parked. parked_at NOW() so the parked-sweep
	// doesn't preempt the test.
	bParkedRunID := shared.UUID(uuid.New())
	pgtest.ExecForTest(ctx, t, d, `
        INSERT INTO rimsky_node_runs
            (id, node_id, executor_name, required_stores, enqueued_at, phase, state, frame_id, parked_at, parked_reason, run_scope_id)
        VALUES ($1, $2, 'stub', ARRAY[]::text[], NOW(), 'parked', 'parked', $3, NOW(), 'await_callback', $4)
    `, bParkedRunID, bN.ID, earlierFrameID, mainScopeID)
	pgtest.ExecForTest(ctx, t, d,
		`UPDATE rimsky_nodes SET frame_id = $1 WHERE id = $2`, earlierFrameID, bN.ID)

	// @deliberate: c has no in-flight row yet — MarkStaleForCascade in the walk
	// will insert one.

	args := runtime.RunArgs{
		Persist: backend, Queue: d.Queue(), Clock: shared.SystemClock{}, Logger: shared.SilentLogger{},
	}
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return runtime.CascadeSubscribersStaleInTxForTest(
			ctx, args, tx, aN.ID, "a", aRunID, inst.ID, frameID,
		)
	}))

	// @deliberate: Verify parked_resume_started fired for b.
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
	_ = cN // @deliberate: silence unused warning if c never gets touched
}

// TestPullHardDepUpstreams_NoExtraWakeForCurrentFrameInFlight verifies
// that when an upstream-refresh upstream already has an in-flight run
// pinned to senderFrameID (pending/active/held — i.e.
// GetInFlightRunForNode returns hasRun=true), pullForceRefreshUpstreams
// MUST NOT re-probe GetParkedByNode and fire wakeParkedReceiverInTx.
// The existing wait-set blocker keys on the existing run id; an
// extra wake would emit a duplicate `parked_resume_started` event
// and churn state-transition surface.
//
// Setup: a (sender), b (upstream already pending in current frame),
// c (receiver with `force_upstream_refresh: true` on b). After the
// cascade walk we assert that NO parked_resume_started event fires
// for b.
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

	// @deliberate: Sender a: active in current frame.
	aRunID := shared.UUID(uuid.New())
	pgtest.ExecForTest(ctx, t, d, `
        INSERT INTO rimsky_node_runs
            (id, node_id, executor_name, required_stores, enqueued_at, phase, state, frame_id, run_scope_id)
        VALUES ($1, $2, $3, ARRAY[]::text[], NOW(), 'active', 'running', $4, $5)
    `, aRunID, aN.ID, "stub", frameID, mainScopeID)
	pgtest.ExecForTest(ctx, t, d,
		`UPDATE rimsky_nodes SET frame_id = $1 WHERE id = $2`, frameID, aN.ID)

	// @deliberate: Upstream b: in-flight pending in CURRENT frame (NOT parked).
	// GetInFlightRunForNode(b, frameID) returns hasRun=true.
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

	// @deliberate: Assert NO parked_resume_started fired for b (it was never parked).
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
	_ = cN // @deliberate: silence unused warning if c never gets touched
}

// makeHardDepTemplate builds a 3-node template (a, b, c) where c
// declares `force_upstream_refresh: true` on b's attribute
// subscription and subscribes to a's terminal/attribute signals.
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
					// @deliberate: Covers the {{nodes.a.attribute.a_value}} read.
					{Node: "a", Type: "attribute/a_value/changed", WakeOnChange: nodepkg.BoolPtr(true), ForceUpstreamRefresh: nodepkg.BoolPtr(false)},
					// @deliberate: Migrated from attribute-field hard_dep: true on b_val.
					// Covers the {{nodes.b.attribute.b_value}} read AND drags
					// b into the frame on c's invalidation.
					{Node: "b", Type: "attribute/b_value/changed", WakeOnChange: nodepkg.BoolPtr(true), ForceUpstreamRefresh: nodepkg.BoolPtr(true)},
				},
				Attributes: &nodepkg.NodeAttributesDef{Schema: cSchema},
			},
		},
	}
}
