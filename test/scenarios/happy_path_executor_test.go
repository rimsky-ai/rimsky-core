// Scenario 1 — happy path: one executor-backed node runs to completion.
package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
)

func TestHappyPathExecutor(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Complete(map[string]any{"ok": true}, true, "initial")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "happy-path", Version: "1",
		Nodes: []node.TemplateNodeDef{
			{Type: "worker", Executor: "stub"},
		},
	})
	iid := h.CreateInstance(tid, "ck-happy", map[string]any{})

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	require.True(t, h.WaitForNodeState(n.ID, shared.NodeStateFresh, 15*time.Second),
		"worker did not reach fresh")

	// Verify a commit (or work_completed) event was appended.
	nid := n.ID
	evs, err := h.Storage.Events().List(h.Ctx, storage.EventListFilter{NodeID: &nid},
		storage.ListPagination{Limit: 200}, nil)
	require.NoError(t, err)
	var sawCompleted bool
	for _, e := range evs.Events {
		if e.Kind == "work_completed" {
			sawCompleted = true
			break
		}
	}
	require.True(t, sawCompleted, "expected work_completed event")
}
