// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario — unused by_match entries surface as 0 in the per-entry
// match counter at instance terminal.
//
// Per concept:attribute the per-entry counter on
// rimsky_instances.attribute_overrides_match_counts is the
// "silent miss becomes loud miss" surface that makes matcher-overlay
// testing safe against producer key-scheme changes. Counters that
// stay at 0 at instance terminal flag matchers that never fired.
package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/graph/node"
	"github.com/rimsky-ai/rimsky-core/graph/scenario"
)

func TestAttributeOverridesMatchOverlayUnused_CounterZeroForNonFiringEntries(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "by-match-unused", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"cli": map[string]any{"type": "object"},
						"ok":  map[string]any{"type": "boolean", "readOnly": true},
					},
				}),
			),
		},
	})

	// Five entries; only #0 (node_type=worker) and #2 (empty matcher)
	// match the worker dispatch. The other three target a child_key
	// the harness never produces (no fan-out), so they never fire.
	overrides := map[string]any{
		"by_match": []any{
			map[string]any{
				"matcher": map[string]any{"node_type": "worker"},
				"overlay": map[string]any{"cli": map[string]any{"tag-0": "fired"}},
			},
			map[string]any{
				"matcher": map[string]any{"node_type": "worker", "child_key": "k1"},
				"overlay": map[string]any{"cli": map[string]any{"tag-1": "fired"}},
			},
			map[string]any{
				"matcher": map[string]any{},
				"overlay": map[string]any{"cli": map[string]any{"tag-2": "fired"}},
			},
			map[string]any{
				"matcher": map[string]any{"child_key": "k2"},
				"overlay": map[string]any{"cli": map[string]any{"tag-3": "fired"}},
			},
			map[string]any{
				"matcher": map[string]any{"executor": "stub", "child_key": "k3"},
				"overlay": map[string]any{"cli": map[string]any{"tag-4": "fired"}},
			},
		},
	}
	iid := h.CreateInstanceWithOverrides(tid, "ck-bm-unused", map[string]any{}, overrides)

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateFresh, 15*time.Second),
		"worker did not reach fresh")

	var inst *persistence.InstanceRow
	require.Eventually(t, func() bool {
		err := h.Persist.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
			r, err := h.Persist.Instances().Get(ctx, iid, tx)
			inst = r
			return err
		})
		if err != nil || inst == nil {
			return false
		}
		c := inst.AttributeOverridesMatchCounts
		return len(c) == 5 && c[0] == 1 && c[1] == 0 && c[2] == 1 && c[3] == 0 && c[4] == 0
	}, 5*time.Second, 50*time.Millisecond,
		"match-counts should be [1, 0, 1, 0, 0]; got=%v",
		func() any {
			if inst == nil {
				return nil
			}
			return inst.AttributeOverridesMatchCounts
		}())
}
