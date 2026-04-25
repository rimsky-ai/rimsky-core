// Scenario 5 — A → B → C all run to fresh; invalidating A cascades to
// B and C, both re-run.
package scenarios

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/shared"
)

func TestCascadeInvalidate(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("a").Complete(map[string]any{"a": 1}, true, "a")
	h.Stub.WhenType("b").Complete(map[string]any{"b": 1}, true, "b")
	h.Stub.WhenType("c").Complete(map[string]any{"c": 1}, true, "c")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "chain", Version: "1",
		Nodes: []node.TemplateNodeDef{
			{Type: "a", Executor: "stub"},
			{Type: "b", Executor: "stub", Dependencies: []string{"a"}},
			{Type: "c", Executor: "stub", Dependencies: []string{"b"}},
		},
	})
	iid := h.CreateInstance(tid, "ck-chain", map[string]any{})

	a := h.FindNode(iid, "a")
	b := h.FindNode(iid, "b")
	c := h.FindNode(iid, "c")
	require.NotNil(t, a)
	require.NotNil(t, b)
	require.NotNil(t, c)

	// All three reach fresh on first run.
	require.True(t, h.WaitForNodeState(a.ID, shared.NodeStateFresh, 15*time.Second))
	require.True(t, h.WaitForNodeState(b.ID, shared.NodeStateFresh, 15*time.Second))
	require.True(t, h.WaitForNodeState(c.ID, shared.NodeStateFresh, 15*time.Second))

	// Invalidate A; B and C should cascade-stale then rerun to fresh.
	resp, err := http.Post(h.ControlBase+"/nodes/"+a.ID.String()+"/invalidate",
		"application/json", bytes.NewReader([]byte(`{}`)))
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Expect B and C to eventually return to fresh (A runs again and cascades).
	require.True(t, h.WaitForNodeState(a.ID, shared.NodeStateFresh, 20*time.Second),
		"a did not re-reach fresh")
	require.True(t, h.WaitForNodeState(b.ID, shared.NodeStateFresh, 20*time.Second),
		"b did not re-reach fresh")
	require.True(t, h.WaitForNodeState(c.ID, shared.NodeStateFresh, 20*time.Second),
		"c did not re-reach fresh")
}
