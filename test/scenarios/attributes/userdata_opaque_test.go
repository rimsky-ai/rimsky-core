// Spec §19.1 + §5.8 + @blessed-invariant 11 — userdata is opaque to
// rimsky.
//
// The node's `userdata` carries `{"prompt": "{{deps.x.value}}"}` — a
// dispatch-syntax `{{...}}` directive that, if rimsky parsed userdata,
// would either substitute or raise template_resolution_failed. Per spec
// §10.2 + §10.3, rimsky never inspects userdata; the directive arrives
// at the executor verbatim.
//
// We assert two things:
//
//  1. The stub executor observes `userdata.prompt == "{{deps.x.value}}"`
//     verbatim (no substitution by rimsky).
//  2. The node still runs (no template_resolution_failed event), proving
//     the substitution machinery did not even attempt to parse the
//     userdata payload.
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

func TestUserdataOpaque(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("worker").Complete(map[string]any{}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "userdata-opaque", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:     "worker",
				Executor: "stub",
				// `{{deps.x.value}}` is a dispatch-time substitution
				// directive in attribute `source:` declarations, but here
				// it sits inside `userdata` — rimsky must NOT touch it.
				Userdata: map[string]any{
					"prompt": "{{deps.x.value}}",
					"nested": map[string]any{
						"also_directive": "{{params.region}}",
					},
				},
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-userdata", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)
	require.True(t, h.WaitForNodeState(worker.ID, shared.NodeStateFresh, 15*time.Second),
		"worker did not reach fresh — userdata should not interfere with dispatch")

	// Executor saw the userdata verbatim.
	var observedUD map[string]any
	for _, obs := range h.Stub.Observed() {
		if obs.NodeID == worker.ID.String() {
			observedUD = obs.Userdata
			break
		}
	}
	require.NotNil(t, observedUD, "stub did not observe a dispatch")
	require.Equal(t, "{{deps.x.value}}", observedUD["prompt"],
		"top-level userdata directive must arrive at the executor verbatim")
	nested, ok := observedUD["nested"].(map[string]any)
	require.True(t, ok, "nested userdata must round-trip as a map")
	require.Equal(t, "{{params.region}}", nested["also_directive"],
		"nested userdata directive must arrive at the executor verbatim")

	// No template_resolution_failed event was emitted (the substitution
	// machinery never even looked at userdata).
	nid := worker.ID
	evs, err := h.Storage.Events().List(h.Ctx,
		storage.EventListFilter{NodeID: &nid, Kind: "template_resolution_failed"},
		storage.ListPagination{Limit: 10}, nil)
	require.NoError(t, err)
	require.Empty(t, evs.Events,
		"rimsky must not parse userdata; no template_resolution_failed expected")
}
