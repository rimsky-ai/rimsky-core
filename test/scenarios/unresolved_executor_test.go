// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario 11 — node declares an executor that is not registered. The
// supervisor's omnibus runner picks the candidate (its accept-list
// contains the executor name), then `Resolver.Resolve` misses, an
// `unresolved_executor` event is emitted, and the terminal classifier
// routes the Errored event through the policy chain. With no template-
// declared policy for `unresolved_executor`, the runner defaults to
// give_up(unknown_error_class) and the node lands in failed.
//
// Migrated to the stores-redesign template grammar (spec §11): the
// node is built via scenario.MakeNode. The legacy
// `action_taken=dispatch_impossible` shape is gone — the redesign routes
// every error class (including unresolved_executor) through the same
// policy-chain path (§7.3 / §11.6). The test runs the harness with
// NoSupervisor and drives `runtime.RunNode` directly so the dispatch
// row's executor_name lives in the supervisor's accept-list while the
// resolver has no entry for it (the §7.3 step 4a resolver-miss branch).
package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/graph/node"
	"github.com/fallguy/rimsky/graph/scenario"
	"github.com/fallguy/rimsky/runtime"
	"github.com/fallguy/rimsky/runtime/executor"
)

func TestUnresolvedExecutor(t *testing.T) {
	t.Parallel()
	// NoSupervisor so we control exactly when the runner picks the
	// candidate; the test re-points the node's executor to a name the
	// resolver does not know about and then drives RunNode once.
	h := scenario.Start(t, scenario.HarnessOpts{NoSupervisor: true})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "ghost", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "ghost", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-ghost", map[string]any{})

	n := h.FindNode(iid, "ghost")
	require.NotNil(t, n)

	// Re-point the node's executor at an unregistered name. The dispatch
	// row keeps executor_name='stub' (matches our AcceptedExecutors below)
	// so the candidate is selected; nd.Executor is what `Resolver.Resolve`
	// is called against, and that lookup misses.
	_, err := h.Pool.Exec(h.Ctx,
		`UPDATE rimsky_nodes SET executor = $1 WHERE id = $2`,
		"does_not_exist_unknown", n.ID,
	)
	require.NoError(t, err)

	pool := executor.NewClientPool()
	t.Cleanup(func() { _ = pool.Close() })

	// Force the dispatch row to a known eligible shape: executor='stub'
	// (in our AcceptedExecutors), claimed_by NULL, enqueued_at a few
	// seconds in the past (so any clock skew between test host and the
	// Postgres container can't push it past NOW()).
	_, err = h.Pool.Exec(h.Ctx,
		`UPDATE rimsky_node_runs
		    SET executor_name = 'stub',
		        required_stores = '{}',
		        claimed_by = NULL,
		        claimed_at = NULL,
		        last_heartbeat_at = NULL,
		        enqueued_at = NOW() - INTERVAL '5 seconds'
		  WHERE node_id = $1`,
		n.ID,
	)
	require.NoError(t, err)

	// Resolver has no entry for "does_not_exist_unknown" → §7.3 step 4a
	// fires. AcceptedExecutors contains "stub" so the dispatch SELECT
	// admits the candidate.
	args := runtime.RunArgs{
		Persist:           h.Persist,
		Queue:             h.Queue,
		ClaimHandles:      h.Persist.ClaimHandles(),
		AdvisoryLocker:    h.Driver.AdvisoryLocker(),
		StoreRegistry:     locks.NewRegistry(),
		Clock:             shared.SystemClock{},
		Logger:            shared.SilentLogger{},
		SupervisorID:      "scenario-runner",
		AcceptedExecutors: []string{"stub"},
		Pool:              pool,
		Resolver:          executor.NewStaticResolver(map[string]executor.Endpoint{}),
		HeartbeatInterval: 100 * time.Millisecond,
	}

	out, err := runtime.RunNode(h.Ctx, args, nil)
	require.NoError(t, err)
	require.True(t, out.Ran, "runner should commit acquisition for the candidate")

	// With no policy declared for unresolved_executor, node.Evaluate
	// defaults to give_up(unknown_error_class) → state failed.
	var got *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().Get(h.Ctx, n.ID, tx)
		got = r
		return err
	}))
	require.Equal(t, cascade.NodeStateFailed, got.State)

	// Verify event trail: unresolved_executor followed by the canonical
	// terminal/error/unresolved_executor signal row (per Pass 5 of spec
	// 2026-05-23-signal-taxonomy-and-policy-decoupling-design the legacy
	// `error` fixed-string audit row retired in favor of the signal
	// type-path).
	nid := n.ID
	var evs persistence.EventListResult
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Events().List(h.Ctx, persistence.EventListFilter{NodeID: &nid},
			persistence.ListPagination{Limit: 500}, tx)
		evs = r
		return err
	}))
	var (
		sawUnresolved          bool
		sawTerminalErrorSignal bool
	)
	for _, e := range evs.Events {
		switch e.Kind {
		case "unresolved_executor":
			sawUnresolved = true
		case "terminal/error/unresolved_executor":
			sawTerminalErrorSignal = true
		}
	}
	require.True(t, sawUnresolved, "expected unresolved_executor event")
	require.True(t, sawTerminalErrorSignal,
		"expected terminal/error/unresolved_executor signal row")
}
