// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenario

import (
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

func TestHarnessSmoke(t *testing.T) {
	t.Parallel()
	h := Start(t, HarnessOpts{})
	h.Stub.WhenType("greet").Success(map[string]any{"ok": true}, true, "hello")

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
	h.WaitForNodeState(greet.ID, cascade.NodeStateFresh)
}

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

func TestTemplateSpecToJSONNewGrammar(t *testing.T) {
	t.Parallel()
	spec := node.TemplateSpec{
		Name:    "grammar-check",
		Version: "1",
		Nodes: []node.TemplateNodeDef{
			MakeNode(
				node.TemplateNodeDef{Type: "consume", Executor: "stub"},
				WithClaimProducers(
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

	require.Contains(t, n, "claim_producers")
	require.Contains(t, n, "locks")
	require.Contains(t, n, "attributes")

	require.NotContains(t, n, "claim_resolutions")
	require.NotContains(t, n, "concurrency_tags")
	require.NotContains(t, n, "owns_resources")
	require.NotContains(t, n, "reads_resources")
	require.NotContains(t, n, "restore_version")

	stores := n["claim_producers"].([]map[string]any)
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
}

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
