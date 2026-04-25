package scenario

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/shared"
)

// TestHarnessSmoke verifies the scenario harness stands up every in-process
// component and a trivial one-node template runs end-to-end against the stub
// executor. Not representative of real scenarios — those live in
// test/scenarios — but protects Start() from regressions.
func TestHarnessSmoke(t *testing.T) {
	t.Parallel()
	h := Start(t, HarnessOpts{})
	h.Stub.WhenType("greet").Complete(map[string]any{"ok": true}, true, "hello")

	tmpl := node.TemplateSpec{
		Name:    "smoke",
		Version: "1",
		Nodes: []node.TemplateNodeDef{
			{Type: "greet", Executor: "stub"},
		},
	}
	tid := h.DeployTemplate(tmpl)
	iid := h.CreateInstance(tid, "smoke-1", map[string]any{})

	greet := h.FindNode(iid, "greet")
	require.NotNil(t, greet)
	require.True(t, h.WaitForNodeState(greet.ID, shared.NodeStateFresh, 10*time.Second),
		"node did not reach fresh within 10s")
}
