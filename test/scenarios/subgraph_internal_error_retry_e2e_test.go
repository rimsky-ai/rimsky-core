// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// S3 must-pass scenario — subgraph_internal_error_retry_e2e.
//
// End-to-end coverage of retry semantics within a sub-graph under the
// RunScope-first reshape per spec
// .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md
// §"Test coverage matrix / S3":
//
//   - A sub-graph internal node errors.
//   - The retry policy fires.
//   - The retry stays WITHIN the sub-graph RunScope (no scope
//     reassignment between retries).
//
// Pins the load-bearing property: retry path within a sub-graph
// context threads the sub-graph RunScope id, not the main RunScope id,
// to Queue.Enqueue.
package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestSubgraphInternalErrorRetryE2E(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	// Stub: caller (entry-absorbed) succeeds; inner-mid errors and
	// retries (the stub returns Error every time; the retry policy
	// fires until Count is exhausted).
	h.Stub.WhenType("caller").Success(map[string]any{"ok": true}, true, "ok")
	h.Stub.WhenType("inner-mid").Error("flaky", map[string]any{"why": "transient"})
	h.Stub.WhenType("inner-exit").Success(map[string]any{"done": true}, true, "exit")

	openAttrs := scenario.WithAttributes(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ok":   map[string]any{"type": "boolean", "readOnly": true},
			"done": map[string]any{"type": "boolean", "readOnly": true},
		},
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "subgraph-internal-error-retry", Version: "1",
		Graphs: []tmplspec.GraphSpec{
			{
				Name: tmplspec.MainGraphName,
				Nodes: []node.TemplateNodeDef{
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "caller", Delegate: "worker"},
						openAttrs,
					),
				},
			},
			{
				Name:  "worker",
				Entry: "inner-entry",
				Exit:  "inner-exit",
				Nodes: []node.TemplateNodeDef{
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "inner-entry", Executor: "stub"},
						openAttrs,
					),
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "inner-mid", Executor: "stub",
							Subscribes: []tmplspec.SubscriptionEntry{{Node: "inner-entry", Type: "terminal/*"}},
							ErrorTypes: map[string]node.ErrorTypePolicy{
								"stub/flaky": {Policy: []node.PolicyAction{{Action: "retry", Count: 2}}},
							},
						},
						openAttrs,
					),
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "inner-exit", Executor: "stub",
							Subscribes: []tmplspec.SubscriptionEntry{{Node: "inner-mid", Type: "terminal/*"}},
						},
						openAttrs,
					),
				},
			},
		},
	})
	iid := h.CreateInstance(tid, "ck-subgraph-internal-error-retry", map[string]any{})

	innerMidNode := h.FindNode(iid, "inner-mid")
	require.NotNil(t, innerMidNode, "inner-mid node missing")

	// Wait for a retry dispatch on inner-mid. The retry dispatch
	// carries PriorDispatchID + PRIOR_RETRY_AFTER_ERROR; the witness
	// the stub captures pins the retry actually fired.
	retrySeen := false
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		for _, o := range h.Stub.Observed() {
			if o.NodeType == "inner-mid" &&
				o.PriorDispatchID != "" &&
				o.PriorDispatchDisposition == genv1.PriorDispatchDisposition_PRIOR_RETRY_AFTER_ERROR {
				retrySeen = true
				break
			}
		}
		if retrySeen {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.True(t, retrySeen,
		"inner-mid retry dispatch should carry PRIOR_RETRY_AFTER_ERROR")

	// All inner-mid runs (original + retries) live in the SAME
	// sub-graph RunScope (graph_name = 'worker'), NOT the main scope.
	var distinctScopes, totalRuns int
	h.QueryRowSQL(`
		SELECT COUNT(DISTINCT r.run_scope_id), COUNT(*)
		  FROM rimsky_node_runs r
		  JOIN rimsky_run_scopes rs ON rs.id = r.run_scope_id
		 WHERE r.node_id = $1
		   AND rs.graph_name = 'worker'
	`, []any{innerMidNode.ID}, &distinctScopes, &totalRuns)
	require.Equal(t, 1, distinctScopes,
		"retry dispatches must stay within the same sub-graph RunScope")
	require.GreaterOrEqual(t, totalRuns, 2,
		"retry should produce at least two rimsky_node_runs rows for inner-mid")

	// The S3 assertion is satisfied by:
	//   1. The retry dispatch was observed (PRIOR_RETRY_AFTER_ERROR) —
	//      the retry path fired through the supervisor's terminal-error
	//      handler, threading the sub-graph RunScope id.
	//   2. All retry rows live in the same sub-graph RunScope —
	//      retries do not escape the sub-graph context.
	// The eventual node state (failed / stale / running) depends on
	// the retry budget's exhaustion timing and is not load-bearing for
	// the RunScope-routing assertion S3 pins.
}
