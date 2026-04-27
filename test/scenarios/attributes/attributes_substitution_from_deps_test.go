// Spec §19.1 — `{{deps.<n>.<f>}}` substitution at dispatch.
//
// Two-node chain: upstream `producer` writes a value into its
// rimsky_node_attributes.data via Complete.attributes_delta; downstream
// `consumer` declares `source: "{{deps.producer.value}}"` on its schema
// property. The supervisor substitutes the upstream's data into the
// dispatched ExecuteRequest at dispatch time and the consumer observes
// the substituted string verbatim (per spec §10: substitution always
// yields a string).
package attributes

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scenario"
)

func TestAttributesSubstitutionFromDeps(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	// producer's executor writes "northwest" into its attributes.data.
	h.Stub.WhenType("producer").Complete(map[string]any{"value": "northwest"}, true, "produced")
	// consumer's executor returns no delta — we only care that its
	// attributes.value is the substituted upstream value.
	h.Stub.WhenType("consumer").Complete(map[string]any{}, true, "consumed")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "attr-deps", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "producer", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type":       "object",
					"properties": map[string]any{"value": map[string]any{"type": "string"}},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "consumer", Executor: "stub", Dependencies: []string{"producer"}},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"value": map[string]any{"type": "string", "source": "{{deps.producer.value}}"},
					},
					"required": []any{"value"},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-attr-deps", map[string]any{})

	cons := h.FindNode(iid, "consumer")
	require.NotNil(t, cons)
	// Under the frame model, nodes start fresh and only transition to
	// stale via frame-start; WaitForNodeState(fresh) would short-circuit
	// before any work runs. Wait for the work_completed event instead.
	require.True(t, h.WaitForEventKind(cons.ID, "work_completed", 30*time.Second),
		"consumer did not emit work_completed")

	// Verify the substituted value landed in the consumer's attributes.data.
	row, err := h.Storage.NodeAttributes().Get(h.Ctx, cons.ID)
	require.NoError(t, err)
	require.NotNil(t, row, "expected node_attributes row for consumer")
	require.Equal(t, "northwest", row.Data["value"],
		"expected consumer.attributes.value to be substituted from deps.producer.value")

	// Verify the executor observed the substituted attributes too.
	var consAttrs map[string]any
	for _, obs := range h.Stub.Observed() {
		if obs.NodeType == "consumer" {
			consAttrs = obs.Attributes
			break
		}
	}
	require.NotNil(t, consAttrs, "stub did not observe a consumer dispatch")
	require.Equal(t, "northwest", consAttrs["value"],
		"executor must receive the substituted attributes object")
}
