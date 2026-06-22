// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
							Subscribes: []tmplspec.SubscriptionEntry{{Node: "inner-entry", Type: "terminal/*", WakeOnChange: tmplspec.BoolPtr(true), ForceUpstreamRefresh: tmplspec.BoolPtr(false)}},
							MaxRetries: node.IntPtr(2),
							ErrorTypes: map[string]node.ErrorTypePolicy{
								"stub/flaky": {Action: "retry"},
							},
						},
						openAttrs,
					),
					scenario.MakeNode(
						node.TemplateNodeDef{Type: "inner-exit", Executor: "stub",
							Subscribes: []tmplspec.SubscriptionEntry{{Node: "inner-mid", Type: "terminal/*", WakeOnChange: tmplspec.BoolPtr(true), ForceUpstreamRefresh: tmplspec.BoolPtr(false)}},
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

}
