// Scenario (stores §19.1) — filesystem direct-mode write succeeds; the
// region lock-holder row is inserted while the node is running and
// removed after commit.
//
// The harness wires the real `core/store/filesystem` factory via
// HarnessOpts.ExtraStoreFactories, configures one filesystem store
// rooted at t.TempDir(), and runs a single executor-backed node that
// declares a write region against the store. The stub executor signs
// off Complete{changed:true}; the supervisor's runner inserts the
// region lock-holder row inside the §13.3 acquisition tx and deletes
// it inside the §13.6 release tx. Asserts:
//   - the node reaches `fresh`,
//   - exactly one rimsky_lock_holders row was emitted while running
//     (observed via the lock_acquired event),
//   - no rimsky_lock_holders rows remain after commit.
//
// This is the smallest end-to-end test that exercises a real filesystem
// store; the larger disjoint/overlapping/read-vs-write scenarios in
// this directory layer concurrency atop the same wiring.
package stores

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	"github.com/fallguy/rimsky/core/store"
	"github.com/fallguy/rimsky/core/store/filesystem"
)

func TestFilesystemDirectWrite(t *testing.T) {
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

	h.Stub.WhenType("writer").Complete(map[string]any{"ok": true}, true, "wrote")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "fs-direct-write", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "writer", Executor: "stub"},
				scenario.WithStores(scenario.RegionRef("content", "items/a.md")),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"ok": map[string]any{"type": "boolean"},
					},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-fs-write", map[string]any{})

	n := h.FindNode(iid, "writer")
	require.NotNil(t, n)
	require.True(t, h.WaitForNodeState(n.ID, shared.NodeStateFresh, 15*time.Second),
		"writer did not reach fresh")

	// lock_acquired event was emitted while running, and the region
	// lock-holder row is gone after the node committed.
	nid := n.ID
	evs, err := h.Storage.Events().List(h.Ctx, storage.EventListFilter{NodeID: &nid},
		storage.ListPagination{Limit: 200}, nil)
	require.NoError(t, err)
	var sawAcquired bool
	for _, e := range evs.Events {
		if e.Kind == "lock_acquired" {
			sawAcquired = true
			break
		}
	}
	require.True(t, sawAcquired, "expected lock_acquired event during run")

	// rimsky_lock_holders should be empty post-commit (§13.6 release tx
	// DELETEs the row claimant-guarded).
	var holderCount int
	err = h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_lock_holders WHERE holder_node_id = $1`,
		n.ID,
	).Scan(&holderCount)
	require.NoError(t, err)
	require.Equal(t, 0, holderCount,
		"expected no rimsky_lock_holders rows for the node after commit")

	// Sanity: the supervisor handed the live filesystem root to the
	// executor (FilesystemDirectHandle.Path == store root).
	got, ok := h.Stores.GetStore("content")
	require.True(t, ok)
	fsStore, ok := got.(*filesystem.Store)
	require.True(t, ok, "expected the registered store to be the filesystem.Store")
	require.Equal(t, root, fsStore.Root(),
		"filesystem store root must match the harness-supplied tempdir")
}
