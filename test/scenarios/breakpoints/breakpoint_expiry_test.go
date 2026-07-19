// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: breakpoint

package breakpoints

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestBreakpointExpiry(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{
		SchedulerTick: 100 * time.Millisecond,
	})
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "ok")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "bp-breakpoint-expiry", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"ok": map[string]any{"type": "boolean", "readOnly": true},
					},
				}),
			),
		},
	})

	iid := createInstanceWithPause(t, h, tid, "ck-breakpoint-expiry", map[string]any{})
	ttl := 1
	bpID := breakpointCreate(t, h, iid, map[string]any{
		"checkpoint":  "before_dispatch",
		"matcher":     map[string]any{"node_type": "worker"},
		"mode":        "pause",
		"ttl_seconds": ttl,
	})
	require.NotNil(t, getBreakpointRow(t, h, bpID),
		"breakpoint should exist immediately after creation")

	require.Eventually(t, func() bool {
		return getBreakpointRow(t, h, bpID) == nil
	}, 5*time.Second, 100*time.Millisecond,
		"breakpoint row should be deleted by SweepExpired within TTL + sweep cadence")

	status, _ := instanceResume(t, h, iid)
	require.Equal(t, http.StatusOK, status)

	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	h.WaitForNodeState(n.ID, cascade.NodeStateFresh)

	var hits []persistence.BreakpointHitRow
	require.NoError(t, h.Persist.Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := h.Persist.BreakpointHits().ListSinceForInstance(ctx, iid, 0, 100, tx)
		hits = r
		return err
	}))
	require.Empty(t, hits,
		"no hit rows should remain after expiry — dispatch ran post-deletion and FK CASCADE removed any prior hits")
}
