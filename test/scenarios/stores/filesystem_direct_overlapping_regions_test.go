// Scenario (stores §19.1) — two nodes whose write globs overlap
// serialise. The supervisor's region-conflict check (§13.3 step 3d →
// filesystem.RegionsConflict) sees the second acquisition as
// conflicting against the first holder's row and bails the candidate;
// the second node only runs after the first releases.
//
// As in the disjoint scenario, the first node uses AsyncAccepted to
// pin in `running`. The test then asserts that the second node never
// reaches `running` while the first is still running, then unpins the
// first and watches the second proceed.
package stores

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/store"
	"github.com/fallguy/rimsky/core/store/filesystem"
)

func TestFilesystemDirectOverlappingRegions(t *testing.T) {
	// Skipped under frame resolution: two roots in one instance run
	// sequentially, hiding the region-conflict serialisation this test
	// asserted. See notes in
	// docs/plans/2026-04-26-frame-resolution-notes.md.
	t.Skip("frame-resolution: two roots in one instance run sequentially")
	t.Parallel()
	root := t.TempDir()
	h := scenario.Start(t, scenario.HarnessOpts{
		ExtraStoreFactories: []store.Factory{filesystem.Factory{}},
		StoresConfig: store.StoresConfig{
			Stores: map[string]map[string]any{
				"content": {
					"kind": "filesystem",
					"mode": "direct",
					"root": root,
				},
			},
		},
	})

	// `first` pins in running via AsyncAccepted; `second` returns a
	// synchronous Complete so once it claims, it terminates inline.
	h.Stub.WhenType("first").AsyncAccepted("ack-first", 5000)
	h.Stub.WhenType("second").Complete(map[string]any{}, true, "ok")

	// Both nodes write into the same overlapping subtree —
	// "shared/x.md" overlaps "shared/**" per the filesystem
	// glob-overlap rules.
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "fs-overlap", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "first", Executor: "stub"},
				scenario.WithStores(scenario.RegionRef("content", "shared/**")),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "second", Executor: "stub"},
				scenario.WithStores(scenario.RegionRef("content", "shared/x.md")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-fs-overlap", map[string]any{})

	first := h.FindNode(iid, "first")
	second := h.FindNode(iid, "second")
	require.NotNil(t, first)
	require.NotNil(t, second)

	// Wait for `first` to enter running.
	require.True(t, h.WaitForNodeState(first.ID, shared.NodeStateRunning, 15*time.Second),
		"first did not reach running")

	// Sanity-poll for ~2 seconds: while `first` is running, `second`
	// must not advance to running. Stale or fresh both indicate it has
	// not yet been claimed.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got1, _ := h.Storage.Nodes().Get(h.Ctx, first.ID, nil)
		got2, _ := h.Storage.Nodes().Get(h.Ctx, second.ID, nil)
		require.NotNil(t, got1)
		require.NotNil(t, got2)
		require.Equal(t, shared.NodeStateRunning, got1.State,
			"first must remain running while we observe second's gating")
		require.NotEqual(t, shared.NodeStateRunning, got2.State,
			"second should not enter running while first holds the overlapping region")
		require.NotEqual(t, shared.NodeStateFresh, got2.State,
			"second should not have completed while first holds the overlapping region")
		time.Sleep(100 * time.Millisecond)
	}

	// Release the first node via the callback. It commits → fresh,
	// region lock-holder row is deleted, second can now claim.
	completeAck(t, h.Supervisor.CallbackAddr(), "ack-first")

	require.True(t, h.WaitForNodeState(first.ID, shared.NodeStateFresh, 15*time.Second),
		"first did not reach fresh after callback")
	require.True(t, h.WaitForNodeState(second.ID, shared.NodeStateFresh, 15*time.Second),
		"second did not reach fresh after first released the overlapping region")
}
