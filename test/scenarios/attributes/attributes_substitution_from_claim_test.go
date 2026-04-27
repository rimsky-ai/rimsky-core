// Spec §19.1 — `{{claim.<store>.payload.<f>}}` substitution at dispatch.
//
// Node has a claim against a stub claim-store seeded with a payload;
// substitution pulls a field out of that payload into the node's
// attributes. The claim payload `{area: "north", subtopic: "otters"}`
// resolves into the node's `area` and `subtopic` attributes via the
// `{{claim.queue.payload.X}}` directives.
package attributes

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/store"
	"github.com/fallguy/rimsky/core/store/stub"
)

func TestAttributesSubstitutionFromClaim(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{
		StoresConfig: store.StoresConfig{
			Stores: map[string]map[string]any{
				"queue": {
					"kind": stub.KindClaimStore,
					"initial_items": []any{
						map[string]any{"area": "north", "subtopic": "otters"},
					},
				},
			},
		},
	})

	h.Stub.WhenType("worker").Complete(map[string]any{}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "attr-claim", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithStores(scenario.ClaimRef("queue")),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"area":     map[string]any{"type": "string", "source": "{{claim.queue.payload.area}}"},
						"subtopic": map[string]any{"type": "string", "source": "{{claim.queue.payload.subtopic}}"},
					},
					"required": []any{"area", "subtopic"},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-attr-claim", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)
	require.True(t, h.WaitForNodeState(worker.ID, shared.NodeStateFresh, 15*time.Second),
		"worker did not reach fresh")

	row, err := h.Storage.NodeAttributes().Get(h.Ctx, worker.ID)
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, "north", row.Data["area"], "expected attributes.area substituted from claim payload")
	require.Equal(t, "otters", row.Data["subtopic"], "expected attributes.subtopic substituted from claim payload")
}
