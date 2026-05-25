// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario — by_match declaration order is load-bearing.
//
// Two entries match the same dispatch; their overlays touch
// overlapping attribute paths. Per concept:attribute's L5
// "later wins on conflict" rule, the later entry's value wins on
// overlapping keys, and non-conflicting paths from both apply.
package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fallguyconsulting/rimsky/foundation/cascade"
	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	"github.com/fallguyconsulting/rimsky/graph/node"
	"github.com/fallguyconsulting/rimsky/graph/scenario"
)

func TestAttributeOverridesMatchOverlayOrder_LaterWins(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "by-match-order", Version: "1",
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

	overrides := map[string]any{
		"by_match": []any{
			map[string]any{
				"matcher": map[string]any{"node_type": "worker"},
				"overlay": map[string]any{
					"cli": map[string]any{
						"shared":     "first",
						"first-only": "yes",
					},
				},
			},
			map[string]any{
				"matcher": map[string]any{"node_type": "worker"},
				"overlay": map[string]any{
					"cli": map[string]any{
						"shared":      "second",
						"second-only": "yes",
					},
				},
			},
		},
	}
	iid := h.CreateInstanceWithOverrides(tid, "ck-bm-order", map[string]any{}, overrides)

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateFresh, 15*time.Second),
		"worker did not reach fresh")

	var got map[string]any
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, o := range h.Stub.Observed() {
			if o.NodeType == "worker" {
				got = o.Attributes
				break
			}
		}
		if got != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.NotNil(t, got, "stub did not record any worker dispatch")

	cli, ok := got["cli"].(map[string]any)
	require.True(t, ok, "attributes.cli missing: %#v", got)
	require.Equal(t, "second", cli["shared"], "later entry must win on conflicting path")
	require.Equal(t, "yes", cli["first-only"], "non-conflicting path from first entry must apply")
	require.Equal(t, "yes", cli["second-only"], "non-conflicting path from second entry must apply")

	// Both entries fired.
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
		return len(c) == 2 && c[0] == 1 && c[1] == 1
	}, 5*time.Second, 50*time.Millisecond, "match-counts should be [1, 1]")
}
