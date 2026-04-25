// Scenario 15 — blessed-invariant: NextState rejects running→running under
// ReasonDispatchClaimed. Verified directly against the storage layer.
package scenarios

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/shared"
)

func TestStateMachineSameStateRejected(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{NoSupervisor: true, NoScheduler: true})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "sm-same", Version: "1",
		Nodes: []node.TemplateNodeDef{
			{Type: "worker", Executor: "stub"},
		},
	})
	iid := h.CreateInstance(tid, "ck-sm", map[string]any{})

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)

	// Force the node into running first (stale→running via dispatch_claimed).
	require.NoError(t, h.Storage.Nodes().UpdateState(h.Ctx, n.ID,
		shared.NodeStateRunning, node.ReasonDispatchClaimed, nil))

	// Attempt running→running under dispatch_claimed. Should fail with
	// ErrIllegalTransition (blessed-invariant §17).
	err := h.Storage.Nodes().UpdateState(h.Ctx, n.ID,
		shared.NodeStateRunning, node.ReasonDispatchClaimed, nil)
	require.Error(t, err)
	require.True(t, errors.Is(err, shared.ErrIllegalTransition),
		"expected ErrIllegalTransition, got %v", err)
}
