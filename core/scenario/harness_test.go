package scenario

import (
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/store"
	"github.com/fallguy/rimsky/core/store/stub"
)

// TestHarnessSmoke verifies the scenario harness stands up every in-process
// component and a trivial one-node template runs end-to-end against the stub
// executor.
func TestHarnessSmoke(t *testing.T) {
	t.Parallel()
	h := Start(t, HarnessOpts{})
	h.Stub.WhenType("greet").Complete(map[string]any{"ok": true}, true, "hello")

	tmpl := node.TemplateSpec{
		Name:            "smoke",
		Version:         "1",
		FrameResolution: node.FrameResolutionSerialQueue,
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

// TestHarnessClockInjection verifies HarnessOpts.Clock is threaded through.
func TestHarnessClockInjection(t *testing.T) {
	t.Parallel()
	clk := shared.NewControllableClock(time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC))
	h := Start(t, HarnessOpts{
		Clock:        clk,
		NoSupervisor: true,
		NoScheduler:  true,
	})
	require.Same(t, clk, h.Clock, "harness should expose the injected clock")
	clk.Advance(5 * time.Minute)
	require.True(t, clk.Now().After(time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)))
}

// TestHarnessStubStoreFactoriesRegistered verifies the harness's Stores
// registry has both stub factories registered out of the box.
func TestHarnessStubStoreFactoriesRegistered(t *testing.T) {
	t.Parallel()
	h := Start(t, HarnessOpts{
		NoSupervisor: true,
		NoScheduler:  true,
		StoresConfig: store.StoresConfig{
			Stores: map[string]map[string]any{
				"fs":    {"kind": stub.KindFilesystem},
				"queue": {"kind": stub.KindPostgres},
			},
		},
	})
	require.NotNil(t, h.Stores, "harness must expose its store registry")
	gotFS, ok := h.Stores.GetStore("fs")
	require.True(t, ok, "expected fs store registered")
	require.Equal(t, stub.KindFilesystem, gotFS.Kind())
	gotQ, ok := h.Stores.GetStore("queue")
	require.True(t, ok, "expected queue store registered")
	require.Equal(t, stub.KindPostgres, gotQ.Kind())
}

// TestTemplateSpecToJSONNewGrammar verifies the redesigned templateSpecToJSON
// emits the new grammar (`stores`, `locks`, `attributes`, `claim_resolutions`,
// `inherits`) and does NOT emit the retired keys.
func TestTemplateSpecToJSONNewGrammar(t *testing.T) {
	t.Parallel()
	spec := node.TemplateSpec{
		Name:            "grammar-check",
		Version:         "1",
		FrameResolution: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			MakeNode(
				node.TemplateNodeDef{Type: "consume", Executor: "stub"},
				WithStores(
					WriteClaimRef("inbound", "@queue"),
					ClaimRef("output", "region-A"),
				),
				WithLocks(MutexLock("global"), CountingLock("limited")),
				WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"item_id": map[string]any{
							"type":   "string",
							"source": "claim.inbound.payload.id",
						},
					},
				}),
				WithClaimResolutions(map[string]node.ClaimResolution{
					"inbound": {OnCommit: "delete", OnGiveUp: "release_to_back"},
				}),
			),
		},
	}
	got := templateSpecToJSON(spec)
	require.Equal(t, "grammar-check", got["name"])
	nodes, ok := got["nodes"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, nodes, 1)
	n := nodes[0]
	require.Equal(t, "consume", n["type"])
	require.Equal(t, "stub", n["executor"])

	require.Contains(t, n, "stores")
	require.Contains(t, n, "locks")
	require.Contains(t, n, "attributes")
	require.Contains(t, n, "claim_resolutions")

	require.NotContains(t, n, "concurrency_tags")
	require.NotContains(t, n, "owns_resources")
	require.NotContains(t, n, "reads_resources")
	require.NotContains(t, n, "restore_version")

	stores := n["stores"].([]map[string]any)
	require.Len(t, stores, 2)
	require.Equal(t, "inbound", stores[0]["name"])
	require.Equal(t, "@queue", stores[0]["selector"])
	require.Equal(t, "rw", stores[0]["intent"])

	locks := n["locks"].([]map[string]any)
	require.Len(t, locks, 2)
	require.Equal(t, "global", locks[0]["name"])
	require.Equal(t, "limited", locks[1]["name"])

	attrs := n["attributes"].(map[string]any)
	schema := attrs["schema"].(map[string]any)
	require.Equal(t, "object", schema["type"])

	crs := n["claim_resolutions"].(map[string]any)
	inbound := crs["inbound"].(map[string]any)
	require.Equal(t, "delete", inbound["on_commit"])
	require.Equal(t, "release_to_back", inbound["on_give_up"])
}

// TestMakeNodeOptions verifies the fluent helpers compose without aliasing
// slice-typed fields across multiple nodes.
func TestMakeNodeOptions(t *testing.T) {
	t.Parallel()
	base := node.TemplateNodeDef{Type: "worker", Executor: "stub"}

	a := MakeNode(base, WithLocks(MutexLock("lockA")))
	b := MakeNode(base, WithLocks(MutexLock("lockB")))

	require.Len(t, a.Locks, 1)
	require.Len(t, b.Locks, 1)
	require.Equal(t, "lockA", a.Locks[0].Name)
	require.Equal(t, "lockB", b.Locks[0].Name)
	require.False(t, reflect.DeepEqual(a.Locks, b.Locks),
		"option helpers must not alias slice state across nodes")
}
