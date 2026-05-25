// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario 4 — root pure-cascade node fans out to N executor downstreams;
// when the root fires at instance creation, every downstream runs.
//
// Migrated to the stores-redesign template grammar (spec §11): nodes are
// constructed via scenario.MakeNode + the fluent helpers. The root is a
// pure-cascade node (no executor, no stores); each child is an
// executor-backed leaf with a per-node attributes schema documenting the
// shape its executor's attributes_delta is expected to take.
//
// Pre-2026-05-15 the root carried `schedule: "* * * * *"` and the test
// poked `rimsky_schedules.next_fire_at` to drive a fire. The 2026-05-15
// plan D7 + E16 + B10 schedule-retirement cascade retired the per-node
// `schedule:` field; cron is now the bundled `sensor-cron`'s concern.
// For pure-cascade fan-out from a root node, the instance-creation
// initial frame is enough to drive the cascade — the root is a root
// (no upstream), so the scenario harness's
// `driveFrameAndEnqueue(instance_id)` fires it on the first tick.
package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguyconsulting/rimsky/foundation/cascade"
	"github.com/fallguyconsulting/rimsky/graph/node"
	"github.com/fallguyconsulting/rimsky/graph/scenario"
)

func TestFanOutPattern(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("child_a").Success(map[string]any{"a": 1}, true, "a")
	h.Stub.WhenType("child_b").Success(map[string]any{"b": 2}, true, "b")
	h.Stub.WhenType("child_c").Success(map[string]any{"c": 3}, true, "c")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "fan-out", Version: "1",
		Nodes: []node.TemplateNodeDef{
			// Pure-cascade root; no executor / no stores. The harness's
			// instance-creation initial-frame fires this root on the
			// first scheduler tick after CreateInstance.
			scenario.MakeNode(node.TemplateNodeDef{Type: "root"}),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "child_a", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "root", Type: "terminal/*"}),
				scenario.WithAttributes(map[string]any{
					"type":       "object",
					"properties": map[string]any{"a": map[string]any{"type": "integer"}},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "child_b", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "root", Type: "terminal/*"}),
				scenario.WithAttributes(map[string]any{
					"type":       "object",
					"properties": map[string]any{"b": map[string]any{"type": "integer"}},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "child_c", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{Node: "root", Type: "terminal/*"}),
				scenario.WithAttributes(map[string]any{
					"type":       "object",
					"properties": map[string]any{"c": map[string]any{"type": "integer"}},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-fanout", map[string]any{})

	root := h.FindNode(iid, "root")
	require.NotNil(t, root)

	// All three children should reach fresh.
	for _, typ := range []string{"child_a", "child_b", "child_c"} {
		c := h.FindNode(iid, typ)
		require.NotNil(t, c, "missing %s", typ)
		require.True(t, h.WaitForNodeState(c.ID, cascade.NodeStateFresh, 30*time.Second),
			"%s did not reach fresh", typ)
	}
}
