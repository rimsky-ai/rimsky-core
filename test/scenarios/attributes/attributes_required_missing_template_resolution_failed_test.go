// Spec §19.1 — required source-directive missing → `template_resolution_failed`.
//
// The downstream node declares a `required` source-driven field whose
// directive resolves against an upstream that didn't write the named
// field. Per spec §10.4 + §10.3, the dispatch-time substitution miss
// raises `template_resolution_failed`; the default policy chain
// (`[{give_up}]`) routes the node to `failed`.
//
// We pin the upstream to write a different field than the directive
// requests so the substitution is guaranteed to miss the required key.
package attributes

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
)

func TestAttributesRequiredMissingTemplateResolutionFailed(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	// upstream writes "value" but downstream's source asks for "missing".
	h.Stub.WhenType("upstream").Complete(map[string]any{"value": "hello"}, true, "u")
	// downstream's executor would return success — but the supervisor
	// short-circuits at substitution before dispatching.
	h.Stub.WhenType("downstream").Complete(map[string]any{}, true, "d")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "attr-missing", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "upstream", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type":       "object",
					"properties": map[string]any{"value": map[string]any{"type": "string"}},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "downstream", Executor: "stub", Dependencies: []string{"upstream"}},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"missing": map[string]any{"type": "string", "source": "{{deps.upstream.missing}}"},
					},
					"required": []any{"missing"},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-missing", map[string]any{})

	upstream := h.FindNode(iid, "upstream")
	downstream := h.FindNode(iid, "downstream")
	require.NotNil(t, upstream)
	require.NotNil(t, downstream)

	// Upstream completes normally.
	require.True(t, h.WaitForNodeState(upstream.ID, shared.NodeStateFresh, 15*time.Second),
		"upstream did not reach fresh")
	// Downstream's substitution misses → template_resolution_failed →
	// default give_up policy → state=failed.
	require.True(t, h.WaitForNodeState(downstream.ID, shared.NodeStateFailed, 20*time.Second),
		"downstream did not reach failed via template_resolution_failed")

	// A template_resolution_failed event was emitted on the downstream.
	nid := downstream.ID
	evs, err := h.Storage.Events().List(h.Ctx,
		storage.EventListFilter{NodeID: &nid, Kind: "template_resolution_failed"},
		storage.ListPagination{Limit: 10}, nil)
	require.NoError(t, err)
	require.NotEmpty(t, evs.Events,
		"expected template_resolution_failed event on downstream")
}
