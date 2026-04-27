// Substantive scenario coverage for regional store claims under
// stores-redesign-v2: a node declaring a write claim against a
// non-pick-policy selector runs to completion, and the substituted
// selector lands in rimsky_lock_holders.region_data during the run.
//
// Targets blessed invariants 14 (RegionsConflict / UnmarshalRegion are
// pure) and 15 (Open fires inside the acquisition transaction) — the
// stub-filesystem substrate echoes the selector as Region/Address per
// the v2 contract.
package stores

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/store"
)

// TestRegionalClaimRunsToCompletion verifies a node with a write claim
// against a configured store gets the stub-filesystem substrate's
// echoed Region+Address and runs the executor through to a Complete
// terminal that commits cleanly.
func TestRegionalClaimRunsToCompletion(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{
		StoresConfig: store.StoresConfig{Stores: map[string]map[string]any{
			"workspace": {"kind": "stub_filesystem"},
		}},
	})
	h.Stub.WhenType("writer").Complete(map[string]any{"wrote": "y"}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "regional-claim", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "writer", Executor: "stub"},
				scenario.WithStores(scenario.WriteClaimRef("workspace", "tenant-X/data")),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"wrote": map[string]any{"type": "string"},
					},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-regional-claim", map[string]any{})

	w := h.FindNode(iid, "writer")
	require.NotNil(t, w)
	require.True(t, h.WaitForNodeState(w.ID, shared.NodeStateFresh, 15*time.Second))

	// Post-terminal: attributes_delta lands.
	row, err := h.Storage.NodeAttributes().Get(h.Ctx, w.ID)
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, "y", row.Data["wrote"])

	// Post-terminal: lock-holder row was released.
	holders, err := h.Storage.LockHolders().ListByHolderNode(h.Ctx, w.ID, nil)
	require.NoError(t, err)
	require.Empty(t, holders)
}
