// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenario

import (
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
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
				WithLocks(MutexLock("global"), MutexLock("limited")),
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

func TestWaitHelpersScopeToOccurrenceOrdinal(t *testing.T) {
	t.Parallel()
	h := Start(t, HarnessOpts{NoSupervisor: true})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "wait-helper-ordinal", Version: "1",
		Nodes: []node.TemplateNodeDef{
			{Type: "worker", Executor: "stub"},
		},
	})
	iid := h.CreateInstance(tid, "ck-wait-helper-ordinal", map[string]any{})
	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)

	h.WaitForDispatchCount(n.ID, 1)
	require.Equal(t, 1, h.DispatchCount(n.ID),
		"first dispatch must count exactly one run, so a want=2 wait cannot pass on the prior run")

	frameID := h.GetRunningFrameID(iid)
	scopeID := h.GetLatestFrameRootRunScopeID(iid)
	dispatchDone := make(chan struct{})
	go func() {
		h.WaitForDispatchCount(n.ID, 2)
		close(dispatchDone)
	}()
	h.ExecSQL(
		`INSERT INTO rimsky_node_runs (id, node_id, executor_name, required_claim_producers, enqueued_at, frame_id, run_scope_id, sequence)
		 VALUES ($1, $2, 'stub', '{}', NOW(), $3, $4, 999)`,
		uuid.New(), n.ID, frameID, scopeID,
	)
	<-dispatchDone
	require.Equal(t, 2, h.DispatchCount(n.ID))

	require.Equal(t, 0, h.EventCount(n.ID, "probe/fired"))
	require.False(t, h.HasEventKind(n.ID, "probe/fired"))
	h.ExecSQL(
		`INSERT INTO rimsky_events (instance_id, node_id, kind, payload) VALUES ($1, $2, 'probe/fired', '{}')`,
		iid, n.ID,
	)
	h.WaitForEventCount(n.ID, "probe/fired", 1)
	require.Equal(t, 1, h.EventCount(n.ID, "probe/fired"),
		"first emission must count exactly one event, so a want=2 wait cannot pass on the prior emission")
	require.True(t, h.HasEventKind(n.ID, "probe/fired"))

	eventDone := make(chan struct{})
	go func() {
		h.WaitForEventCount(n.ID, "probe/fired", 2)
		close(eventDone)
	}()
	h.ExecSQL(
		`INSERT INTO rimsky_events (instance_id, node_id, kind, payload) VALUES ($1, $2, 'probe/fired', '{}')`,
		iid, n.ID,
	)
	<-eventDone
	require.Equal(t, 2, h.EventCount(n.ID, "probe/fired"))
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
