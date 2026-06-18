// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: breakpoint

package breakpoints

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestConcurrentFrameCorrectness(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker_a").Success(map[string]any{"a_ok": true}, true, "a")
	h.Stub.WhenType("worker_b").Success(map[string]any{"b_ok": true}, true, "b")

	openSchema := func(field string) map[string]any {
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				field: map[string]any{"type": "boolean", "readOnly": true},
			},
		}
	}

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "bp-concurrent-frame", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker_a", Executor: "stub"},
				scenario.WithAttributes(openSchema("a_ok")),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker_b", Executor: "stub"},
				scenario.WithAttributes(openSchema("b_ok")),
			),
		},
	})

	iid := createInstanceWithPause(t, h, tid, "ck-concurrent-frame", map[string]any{})
	bpID := breakpointCreate(t, h, iid, map[string]any{
		"checkpoint": "before_dispatch",
		"matcher":    map[string]any{"node_type": "worker_a"},
	})
	status, _ := instanceResume(t, h, iid)
	require.Equal(t, http.StatusOK, status)

	hit := waitForHitOnBreakpoint(t, h, bpID, 10*time.Second)

	nb := h.FindNode(iid, "worker_b")
	require.NotNil(t, nb)
	if !h.WaitForNodeState(nb.ID, cascade.NodeStateFresh, 15*time.Second) {
		h.QuerySQL(`
			SELECT r.id::text, n.node_type, r.phase::text, r.state::text, r.claimed_by
			  FROM rimsky_node_runs r
			  JOIN rimsky_nodes n ON n.id = r.node_id
			 WHERE n.instance_id = $1
			 ORDER BY r.enqueued_at
		`, []any{iid}, func(scan func(...any) error) error {
			var id, nt, phase, state string
			var claimedBy *string
			if err := scan(&id, &nt, &phase, &state, &claimedBy); err != nil {
				return err
			}
			cb := "<nil>"
			if claimedBy != nil {
				cb = *claimedBy
			}
			t.Logf("  run id=%s node_type=%s phase=%s state=%s claimed_by=%s",
				id, nt, phase, state, cb)
			return nil
		})
		t.Logf("stub.Observed (%d entries):", len(h.Stub.Observed()))
		for i, o := range h.Stub.Observed() {
			t.Logf("  [%d] node_type=%s", i, o.NodeType)
		}
		t.Fatalf("worker_b must complete while worker_a remains paused at the breakpoint")
	}
	require.GreaterOrEqual(t, stubObservedCount(h, "worker_b"), 1,
		"executor must observe worker_b dispatch during the pause window")
	require.Equal(t, 0, stubObservedCount(h, "worker_a"),
		"executor must NOT observe worker_a while paused at the breakpoint")

	status, _ = breakpointResume(t, h, iid, bpID, map[string]any{
		"hit_id": hit.ID.String(),
	})
	require.Equal(t, http.StatusOK, status)

	na := h.FindNode(iid, "worker_a")
	require.NotNil(t, na)
	require.True(t, h.WaitForNodeState(na.ID, cascade.NodeStateFresh, 15*time.Second),
		"worker_a should reach Fresh after the breakpoint resume")
	require.GreaterOrEqual(t, stubObservedCount(h, "worker_a"), 1,
		"executor must observe worker_a dispatch post-resume")
}
