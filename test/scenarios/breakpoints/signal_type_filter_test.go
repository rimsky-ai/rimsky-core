// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: breakpoint

package breakpoints

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestSignalTypeFilter(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("ok_worker").Success(map[string]any{"ok": true}, true, "ok")
	h.Stub.WhenType("err_worker").Error("boom", map[string]any{"why": "nope"})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "bp-signal-type-filter", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "ok_worker", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"ok": map[string]any{"type": "boolean", "readOnly": true},
					},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "err_worker",
					Executor: "stub",
					ErrorTypes: map[string]node.ErrorTypePolicy{
						"stub/boom": {Action: "give_up"},
					},
				},
			),
		},
	})

	iid := createInstanceWithPause(t, h, tid, "ck-signal-type-filter", map[string]any{})
	signalType := "terminal/error/*"
	bpID := breakpointCreate(t, h, iid, map[string]any{
		"checkpoint":  "after_terminal",
		"signal_type": signalType,
		"mode":        "notify_only",
		"matcher":     map[string]any{},
	})
	status, _ := instanceResume(t, h, iid)
	require.Equal(t, http.StatusOK, status)

	okN := h.FindNode(iid, "ok_worker")
	errN := h.FindNode(iid, "err_worker")
	require.NotNil(t, okN)
	require.NotNil(t, errN)
	h.WaitForNodeState(okN.ID, cascade.NodeStateFresh)
	h.WaitForNodeState(errN.ID, cascade.NodeStateFailed)

	time.Sleep(500 * time.Millisecond)

	hits := waitForHitCount(t, h, bpID, 1)
	require.Len(t, hits, 1,
		"signal_type=terminal/error/* should match only the error terminal; got %d hits", len(hits))
	require.Equal(t, "after_terminal", string(hits[0].Checkpoint),
		"the recorded hit must be from the after_terminal checkpoint")

	ts, _ := hits[0].Snapshot["terminal_signal"].(map[string]any)
	require.NotNil(t, ts, "snapshot must carry terminal_signal for after_terminal hits")
	typ, _ := ts["type"].(string)
	require.True(t, strings.HasPrefix(typ, "terminal/error/"),
		"recorded signal type %q should start with terminal/error/", typ)
}
