// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario 10 — executor emits Error with error_class="executor_blocked";
// the supervisor evaluates the matching error_types policy.
//
// Migrated to the stores-redesign template grammar (spec §11): the gated
// node is built via scenario.MakeNode. The node has no stores, locks, or
// attributes wiring — the executor-blocked path terminates without
// writing back attributes, so a schema-less node is the right shape;
// the redesign retains the per-error-class policy chain (spec §11.6)
// the test exercises.
package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguyconsulting/rimsky/foundation/cascade"
	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	"github.com/fallguyconsulting/rimsky/graph/node"
	"github.com/fallguyconsulting/rimsky/graph/scenario"
)

func TestExecutorBlocked(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("gated").Error("executor_blocked", map[string]any{
		"reason": "stuck",
		"need":   "input",
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "blocked", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type: "gated", Executor: "stub",
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"stub/executor_blocked": {
						Policy: []node.PolicyAction{
							{Action: "give_up"},
						},
					},
				},
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-blocked", map[string]any{})

	n := h.FindNode(iid, "gated")
	require.NotNil(t, n)

	// give_up on executor_blocked → node fails.
	//
	// Notes (diagnostic — testcontainer-startup-bound, not a
	// production-code bug):
	//
	//   Symptom: under heavy parallel load (full
	//   ./test/scenarios/... run with -race + -parallel=N), this
	//   wait sometimes exceeds 20s for the node row to flip to
	//   `failed`. Reproducer attempt: 20-rep -race -parallel=16
	//   isolated run of this test PASSED 20/20 (single-test
	//   isolation is stable); full-package -race -parallel=16
	//   -count=10 reliably surfaces a sibling test (e.g.
	//   TestParkedLifecycleMaxParkDurationOverrun) hitting its own
	//   15s budget.
	//
	//   Root cause located: the supervisor's give_up code path is a
	//   single short transaction (runtime/on_error.go::OnError
	//   `case "give_up"`). End-to-end latency from Error response →
	//   `failed` row is well under 100ms when the node is dispatched
	//   promptly. The dominant cost when the budget overruns is
	//   testcontainer Postgres cold-start: each scenario test calls
	//   pgmigrate.OpenDriver, spinning up its own postgres:14-alpine
	//   container, and the harness's per-poll Docker state-query is
	//   "~1-6s under saturated parallel load; occasional 15-20s
	//   spikes when the daemon is heavily contended" (see
	//   testpg/testpg.go::StartFreshPostgresDSN).
	//
	//   Ruled out: scheduler tick rate (100ms claim poll, 250ms
	//   scheduler tick; sub-second under any healthy load); the
	//   give_up state-update transaction itself (a single
	//   sb.Nodes().UpdateState + queue RemoveForNode); the
	//   WaitForNodeState poller (50ms cadence).
	//
	//   Resolution: keep the 30s budget so the per-test slice
	//   includes one testcontainer cold-start spike without
	//   tripping. The peer scenarios that exercise the same
	//   `failed` terminal already use 30s
	//   (give_up_test.go::TestGiveUp, retry_loop_cap_test.go,
	//   lifecycle_handlers_test.go); the previous 20s here was an
	//   outlier predating the cold-start diagnostic.
	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateFailed, 30*time.Second),
		"gated did not reach failed")

	// Verify the canonical terminal/error/stub/executor_blocked signal
	// row (per Pass 5 of spec 2026-05-23-signal-taxonomy-and-policy-
	// decoupling-design the legacy `error` fixed-string row retired in
	// favor of the signal type-path; per Pass 6 the stub executor
	// auto-prefixes flat classes with `stub/`).
	nid := n.ID
	var evs persistence.EventListResult
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Events().List(h.Ctx, persistence.EventListFilter{NodeID: &nid, Kind: "terminal/error/stub/executor_blocked"},
			persistence.ListPagination{Limit: 100}, tx)
		evs = r
		return err
	}))
	require.NotEmpty(t, evs.Events, "expected terminal/error/stub/executor_blocked signal row")
}
