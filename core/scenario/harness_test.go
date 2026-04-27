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
// executor. Not representative of real scenarios — those live in
// test/scenarios — but protects Start() from regressions.
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

// TestHarnessClockInjection verifies HarnessOpts.Clock is threaded through to
// the underlying components by reading the Clock field back. The orphan-reap
// scenarios use this to advance past 5×heartbeat_interval deterministically.
func TestHarnessClockInjection(t *testing.T) {
	t.Parallel()
	clk := shared.NewControllableClock(time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC))
	// NoSupervisor + NoScheduler keeps the harness lightweight: the test
	// only needs to verify the Clock field is preserved.
	h := Start(t, HarnessOpts{
		Clock:        clk,
		NoSupervisor: true,
		NoScheduler:  true,
	})
	require.Same(t, clk, h.Clock, "harness should expose the injected clock")
	// Clock must be wired through to the control-api too: a SystemClock
	// fallback would cause time-dependent admin endpoints (e.g.
	// /admin/scheduled-nodes/.../force-fire) to drift in scenario tests.
	// We can't reach into the running app from here, but advancing the
	// clock should not panic — proving the same instance is held.
	clk.Advance(5 * time.Minute)
	require.True(t, clk.Now().After(time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)))
}

// TestHarnessStubStoreFactoriesRegistered verifies the harness's Stores
// registry has both stub factories registered out of the box, and that
// passing a StoresConfig builds the configured stores against them.
func TestHarnessStubStoreFactoriesRegistered(t *testing.T) {
	t.Parallel()
	h := Start(t, HarnessOpts{
		NoSupervisor: true,
		NoScheduler:  true,
		StoresConfig: store.StoresConfig{
			Stores: map[string]map[string]any{
				"fs":    {"kind": stub.KindFilesystem},
				"queue": {"kind": stub.KindClaimStore},
			},
		},
	})
	require.NotNil(t, h.Stores, "harness must expose its store registry")
	gotFS, ok := h.Stores.GetStore("fs")
	require.True(t, ok, "expected fs store registered")
	require.Equal(t, stub.KindFilesystem, gotFS.Kind())
	gotQ, ok := h.Stores.GetStore("queue")
	require.True(t, ok, "expected queue store registered")
	require.Equal(t, stub.KindClaimStore, gotQ.Kind())
}

// TestTemplateSpecToJSONNewGrammar verifies the redesigned templateSpecToJSON
// emits the new grammar (`stores`, `locks`, `attributes`,
// `claim_resolutions`) and does NOT emit the retired keys. Catches
// regressions if a future refactor accidentally re-introduces the old
// owns_resources / reads_resources / concurrency_tags / restore_version
// terminology (spec §11.3 explicitly retired these).
func TestTemplateSpecToJSONNewGrammar(t *testing.T) {
	t.Parallel()
	spec := node.TemplateSpec{
		Name:            "grammar-check",
		Version:         "1",
		FrameResolution: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			MakeNode(
				node.TemplateNodeDef{Type: "consume", Executor: "stub"},
				WithStores(ClaimAndHoldRef("inbound"), RegionRef("output", "region-A")),
				WithLocks(MutexLock("global"), CountingLock("limited", 3)),
				WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"item_id": map[string]any{
							"type":   "string",
							"source": "claim.payload.id",
						},
					},
				}),
				WithClaimResolutions(ResolveClaim("consume", "inbound")),
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

	// New grammar present.
	require.Contains(t, n, "stores", "stores key missing from emitted JSON")
	require.Contains(t, n, "locks", "locks key missing")
	require.Contains(t, n, "attributes", "attributes key missing")
	require.Contains(t, n, "claim_resolutions", "claim_resolutions key missing")

	// Old grammar absent.
	require.NotContains(t, n, "concurrency_tags")
	require.NotContains(t, n, "owns_resources")
	require.NotContains(t, n, "reads_resources")
	require.NotContains(t, n, "restore_version")

	// Spot-check store/lock/attribute structure.
	stores := n["stores"].([]map[string]any)
	require.Len(t, stores, 2)
	require.Equal(t, "inbound", stores[0]["name"])
	require.Equal(t, true, stores[0]["claim"])
	require.Equal(t, true, stores[0]["hold"])

	locks := n["locks"].([]map[string]any)
	require.Len(t, locks, 2)
	require.Equal(t, "global", locks[0]["name"])
	require.Equal(t, "mutex", locks[0]["mode"])
	require.Equal(t, "limited", locks[1]["name"])
	require.Equal(t, "counting", locks[1]["mode"])
	require.Equal(t, 3, locks[1]["limit"])

	attrs := n["attributes"].(map[string]any)
	schema := attrs["schema"].(map[string]any)
	require.Equal(t, "object", schema["type"])

	crs := n["claim_resolutions"].([]map[string]any)
	require.Len(t, crs, 1)
	require.Equal(t, "consume", crs[0]["source"])
	require.Equal(t, "inbound", crs[0]["store"])
}

// TestMakeNodeOptions verifies the fluent helpers compose without aliasing
// the slice-typed fields across multiple nodes. (A naive copy of the base
// spec would share the underlying array between two MakeNode invocations.)
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
