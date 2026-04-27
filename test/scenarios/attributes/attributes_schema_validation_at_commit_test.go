// Spec §19.1 — schema validation at commit (type mismatch in
// executor-populated field) → `attributes_schema_failed`.
//
// The node's schema declares an executor-populated field `count` typed
// `integer`; the stub script returns a non-integer value (a string).
// The supervisor's commit-time validation (spec §5.7.1, §17.1 step 6c)
// catches the type mismatch and routes through the
// `attributes_schema_failed` policy chain (default `[give_up]` → state
// `failed`).
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

func TestAttributesSchemaValidationAtCommit(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	// Schema says "count" is an integer; the stub returns a string.
	h.Stub.WhenType("worker").Complete(map[string]any{"count": "not-an-integer"}, true, "bad")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "attr-schema-fail", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				// `count` is executor-populated (no `source:` directive). At
				// dispatch the field is absent from `data` (no required-list
				// entry → validation passes). At commit the executor's
				// returned non-integer value violates the type constraint.
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"count": map[string]any{"type": "integer"},
					},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-schema-fail", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)
	// Node fails via attributes_schema_failed → default give_up policy.
	require.True(t, h.WaitForNodeState(worker.ID, shared.NodeStateFailed, 20*time.Second),
		"worker did not reach failed via attributes_schema_failed")

	nid := worker.ID
	evs, err := h.Storage.Events().List(h.Ctx,
		storage.EventListFilter{NodeID: &nid, Kind: "attributes_schema_failed"},
		storage.ListPagination{Limit: 10}, nil)
	require.NoError(t, err)
	require.NotEmpty(t, evs.Events,
		"expected attributes_schema_failed event")
}
