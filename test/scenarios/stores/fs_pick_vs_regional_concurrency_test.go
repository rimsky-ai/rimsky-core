// Pick-policy vs regional concurrency. A pick-policy claim and a
// regional claim both target the same folder. Region bytes match
// byte-equal; rimsky's conflict predicate serializes. Both nodes
// eventually reach `fresh` in some order.
package stores

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/foundation/locks"
	"github.com/fallguy/rimsky/modeling/config"
	"github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/scenario"
	"github.com/fallguy/rimsky/modeling/shared"
	fsstore "github.com/fallguy/rimsky/stores/filesystem/store"
	fsfixture "github.com/fallguy/rimsky/stores/filesystem/testfixture"
)

func TestFsPickVsRegionalConcurrency(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "docs", "alpha"), 0o755))

	pp := &fsstore.PickPolicy{
		Root: "docs", OnCommitDefault: "release_to_back",
		OnGiveUpDefault:   "release_to_back",
		VisibilityTimeout: time.Minute, SyncStrategy: "on_open",
	}
	grpcEndpoint, _, teardown := fsfixture.Start(t, fsfixture.Config{
		Root:         root,
		PickPolicies: map[string]*fsstore.PickPolicy{"@r": pp},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"docs": {
					Endpoint:     "grpc://" + grpcEndpoint,
					Capabilities: locks.Capabilities{WriteSemanticsEnvelope: []locks.WriteSemantics{locks.WriteSemanticsSync}},
				},
			},
		},
	})
	h.Stub.WhenType("pick-worker").Complete(map[string]any{}, true, "scenario")
	h.Stub.WhenType("regional-worker").Complete(map[string]any{}, true, "scenario")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "fs-pick-vs-regional", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "pick-worker", Executor: "stub"},
				scenario.WithStores(scenario.WriteClaimRef("docs", "@r")),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "regional-worker", Executor: "stub"},
				scenario.WithStores(scenario.WriteClaimRef("docs", "docs/alpha")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-fs-pick-vs-reg", map[string]any{})

	np := h.FindNode(iid, "pick-worker")
	nr := h.FindNode(iid, "regional-worker")
	require.NotNil(t, np)
	require.NotNil(t, nr)

	require.True(t, h.WaitForNodeState(np.ID, shared.NodeStateFresh, 30*time.Second),
		"pick-worker did not reach fresh")
	require.True(t, h.WaitForNodeState(nr.ID, shared.NodeStateFresh, 30*time.Second),
		"regional-worker did not reach fresh")
}
