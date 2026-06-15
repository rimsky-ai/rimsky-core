// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Scenario — pins spec §10.2 "Signal-type filter on after_terminal":
//
//   - Install an after_terminal breakpoint with signal_type =
//     "terminal/error/*" (trailing-wildcard prefix match).
//   - Run a successful dispatch — terminal/success is the emitted
//     signal; the breakpoint must NOT fire (no hit row for that
//     terminal).
//   - Run a failing dispatch — terminal/error/stub/<class> is emitted;
//     the breakpoint MUST fire (one hit row).
//
// Pins the spec §4.5 prefix-match filter at the supervisor's
// after_terminal checkpoint. notify_only keeps the dispatch from
// blocking on the hit so terminal-state observation stays clean.
//
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
						// @deliberate: give_up immediately so the terminal signal is a
						// single terminal/error/stub/boom (no retry/transient
						// noise to dilute the match assertion).
						"stub/boom": {Policy: []node.PolicyAction{{Action: "give_up"}}},
					},
				},
			),
		},
	})

	iid := createInstanceWithPause(t, h, tid, "ck-signal-type-filter", map[string]any{})
	// @deliberate: Filter: after_terminal + signal_type=terminal/error/* + notify_only +
	// empty matcher (fires on every dispatch's terminal regardless of node).
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
	require.True(t, h.WaitForNodeState(okN.ID, cascade.NodeStateFresh, 15*time.Second),
		"ok_worker should reach Fresh")
	require.True(t, h.WaitForNodeState(errN.ID, cascade.NodeStateFailed, 15*time.Second),
		"err_worker should reach Failed after give_up")

	// @deliberate: The after_terminal checkpoint fires after the terminal-handler tx
	// commits — give the eval+write a brief window.
	time.Sleep(500 * time.Millisecond)

	hits := waitForHitCount(t, h, bpID, 1, 5*time.Second)
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
