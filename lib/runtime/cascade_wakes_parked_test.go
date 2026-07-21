// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	nodepkg "github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/test/support/pgdbtest"
)

func TestCascadeWalk_WakesParkedReceiverInTx(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	spec := nodepkg.TemplateSpec{
		Name: "cascade-wakes-parked-" + uuid.NewString(), Version: "1",
		Nodes: []nodepkg.TemplateNodeDef{
			{Type: "a", Executor: "stub"},
			{
				Type: "b", Executor: "stub",
				Subscribes: []nodepkg.SubscriptionEntry{
					{Node: "a", Type: "terminal/*", ForceUpstreamRefresh: nodepkg.BoolPtr(false)},
				},
			},
		},
	}
	tpl := insertDeployedTemplate(ctx, t, backend, spec)

	ck := "ck-" + uuid.NewString()
	var (
		inst        persistence.InstanceRow
		aN, bN      persistence.NodeRow
		mainScopeID shared.UUID
	)
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, ms := seedInstanceWithMainScope(ctx, t, backend, tx, tpl.ID, &ck)
		inst = i
		mainScopeID = ms
		for _, def := range spec.Nodes {
			n, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
				ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: def.Type, Executor: def.Executor,
			}, tx)
			if err != nil {
				return err
			}
			if def.Type == "a" {
				aN = n
			} else {
				bN = n
			}
		}
		return nil
	}))

	frameID := seedFrame(ctx, t, backend, inst.ID, mainScopeID)

	aRunID := shared.UUID(uuid.New())
	pgdbtest.ExecForTest(ctx, t, d, `
        INSERT INTO rimsky_node_runs
            (id, node_id, executor_name, required_stores, enqueued_at, state, sequence, creation_reason, frame_id, run_scope_id)
        VALUES ($1, $2, 'stub', ARRAY[]::text[], NOW(), 'running', 100, 'cascade', $3, $4)
    `, aRunID, aN.ID, frameID, mainScopeID)

	parkedRunID := shared.UUID(uuid.New())
	resumeAt := time.Now().UTC().Add(6 * time.Hour)
	pgdbtest.ExecForTest(ctx, t, d, `
        INSERT INTO rimsky_node_runs
            (id, node_id, executor_name, required_stores, enqueued_at, state, sequence, creation_reason, frame_id, run_scope_id, parked_at, resume_at)
        VALUES ($1, $2, 'stub', ARRAY[]::text[], NOW(), 'parked', 100, 'cascade', $3, $4, NOW(), $5)
    `, parkedRunID, bN.ID, frameID, mainScopeID, resumeAt)

	args := runtime.RunArgs{
		Persist: backend, Queue: d.Queue(),
		Clock: shared.SystemClock{}, Logger: shared.SilentLogger{},
		SupervisorID: "sup-cascade-wake",
	}
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return runtime.CascadeSubscribersStaleInTxForTest(
			ctx, args, tx, aN.ID, "a", aRunID, inst.ID, frameID,
		)
	}))

	var (
		state       string
		gotResumeAt *time.Time
	)
	pgdbtest.QueryRowForTest(ctx, t, d,
		`SELECT state, resume_at FROM rimsky_node_runs WHERE id = $1`,
		[]any{parkedRunID}, &state, &gotResumeAt)
	require.Equal(t, "stale", state,
		"a parked receiver woken by an upstream cascade must transition parked -> stale in the walker's transaction")
	require.Nil(t, gotResumeAt, "the wake must clear resume_at (single wake path)")

	var events persistence.EventListResult
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := backend.Events().List(ctx, persistence.EventListFilter{NodeID: &bN.ID},
			persistence.ListPagination{Limit: 50}, tx)
		events = r
		return err
	}))
	foundWake := false
	for _, e := range events.Events {
		if e.Kind.String() == "parked_resume_started" {
			foundWake = true
			require.Equal(t, string(runtime.WakeUpstreamCascade), e.Payload["resume_reason"],
				"the wake event must carry the upstream_cascade resume reason")
		}
	}
	require.True(t, foundWake, "the cascade wake must append the parked_resume_started event in the same tx")

	var pendingCount int
	pgdbtest.QueryRowForTest(ctx, t, d,
		`SELECT count(*) FROM rimsky_node_runs WHERE node_id = $1 AND state = 'pending'`,
		[]any{bN.ID}, &pendingCount)
	require.Equal(t, 1, pendingCount,
		"the walk still queues the cascade round as a new pending receiver run carrying the wait-set entry")

	var waitRows int
	pgdbtest.QueryRowForTest(ctx, t, d,
		`SELECT count(*) FROM rimsky_wait_set w
		  JOIN rimsky_node_runs r ON r.id = w.receiver_run_id
		 WHERE w.frame_id = $1 AND w.sender_run_id = $2 AND r.node_id = $3`,
		[]any{frameID, aRunID, bN.ID}, &waitRows)
	require.Equal(t, 1, waitRows,
		"the cascade round must be pinned via a wait-set row keyed by the sender run")
}

func TestCascadeWalk_NoWakeForUnsubscribedParkedNode(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	backend := d.Tables()

	spec := nodepkg.TemplateSpec{
		Name: "cascade-no-wake-unsubscribed-" + uuid.NewString(), Version: "1",
		Nodes: []nodepkg.TemplateNodeDef{
			{Type: "a", Executor: "stub"},
			{Type: "loner", Executor: "stub"},
		},
	}
	tpl := insertDeployedTemplate(ctx, t, backend, spec)

	ck := "ck-" + uuid.NewString()
	var (
		inst        persistence.InstanceRow
		aN, lonerN  persistence.NodeRow
		mainScopeID shared.UUID
	)
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, ms := seedInstanceWithMainScope(ctx, t, backend, tx, tpl.ID, &ck)
		inst = i
		mainScopeID = ms
		for _, def := range spec.Nodes {
			n, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
				ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: def.Type, Executor: def.Executor,
			}, tx)
			if err != nil {
				return err
			}
			if def.Type == "a" {
				aN = n
			} else {
				lonerN = n
			}
		}
		return nil
	}))

	frameID := seedFrame(ctx, t, backend, inst.ID, mainScopeID)

	aRunID := shared.UUID(uuid.New())
	pgdbtest.ExecForTest(ctx, t, d, `
        INSERT INTO rimsky_node_runs
            (id, node_id, executor_name, required_stores, enqueued_at, state, sequence, creation_reason, frame_id, run_scope_id)
        VALUES ($1, $2, 'stub', ARRAY[]::text[], NOW(), 'running', 100, 'cascade', $3, $4)
    `, aRunID, aN.ID, frameID, mainScopeID)

	parkedRunID := shared.UUID(uuid.New())
	pgdbtest.ExecForTest(ctx, t, d, `
        INSERT INTO rimsky_node_runs
            (id, node_id, executor_name, required_stores, enqueued_at, state, sequence, creation_reason, frame_id, run_scope_id, parked_at, resume_at)
        VALUES ($1, $2, 'stub', ARRAY[]::text[], NOW(), 'parked', 100, 'cascade', $3, $4, NOW(), NOW() + interval '6 hours')
    `, parkedRunID, lonerN.ID, frameID, mainScopeID)

	args := runtime.RunArgs{
		Persist: backend, Queue: d.Queue(),
		Clock: shared.SystemClock{}, Logger: shared.SilentLogger{},
		SupervisorID: "sup-cascade-wake",
	}
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return runtime.CascadeSubscribersStaleInTxForTest(
			ctx, args, tx, aN.ID, "a", aRunID, inst.ID, frameID,
		)
	}))

	var state string
	pgdbtest.QueryRowForTest(ctx, t, d,
		`SELECT state FROM rimsky_node_runs WHERE id = $1`, []any{parkedRunID}, &state)
	require.Equal(t, "parked", state,
		"only subscribed receivers wake on an upstream cascade; an unsubscribed parked node stays parked")
}
