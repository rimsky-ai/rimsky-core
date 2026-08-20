// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scenarios

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// @concept: parked-state
// @concept: cascade-mode
// @story: resume-preserves-snapshot
// @story: cascade-defers-during-flight
func TestParkedRunSurvivesMostRecentCascadeRound(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("upstream").Success(map[string]any{"x": "r1"}, true, "upstream-r1")

	resumeAt := time.Now().Add(1 * time.Hour)
	h.Stub.WhenType("worker").
		Park(resumeAt).
		Then().Success(map[string]any{}, true, "worker-woken-row").
		Then().Success(map[string]any{}, true, "worker-new-round")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "parked-run-survives-most-recent", Version: "1",
		Messages: []spec.MessageSchema{
			{Type: "test/wake"},
		},
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "upstream", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "test/wake", Type: "terminal/success",
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
				scenario.WithAttributes(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"x": map[string]any{"type": "string", "readOnly": true},
					},
					"required": []any{"x"},
				}),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:        "worker",
					Executor:    "stub",
					CascadeMode: spec.CascadeModeMostRecent,
				},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "upstream", Type: "terminal/success",
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
			),
		},
	})

	iid := h.CreateInstance(tid, "ck-parked-survives-most-recent", map[string]any{})
	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	h.PostInstanceMessage(iid, "test/wake", nil, "parked-survives-kick")
	h.WaitForNodeState(worker.ID, cascade.NodeStateParked)

	var parkedRunID string
	awaited.Until(t, "the worker's parked run row to appear", func() bool {
		h.QuerySQL(`SELECT id::text FROM rimsky_node_runs WHERE node_id = $1 AND state = 'parked'`,
			[]any{worker.ID}, func(scan func(...any) error) error {
				return scan(&parkedRunID)
			})
		return parkedRunID != ""
	})

	pauseResp := postJSON(t,
		h.ControlBase+fmt.Sprintf("/v1/instances/%s/pause", iid.String()),
		map[string]any{})
	require.Equal(t, http.StatusOK, pauseResp.status,
		"pause must succeed so the second upstream round starts on the test's signal; body=%s",
		string(pauseResp.raw))

	h.Stub.WhenType("upstream").Success(map[string]any{"x": "r2"}, true, "upstream-r2")

	invalidateResp := postDebugOverride(t, h.ControlBase, iid, map[string]any{
		"action":    "invalidate_node",
		"node_type": "upstream",
	}, "")
	require.Equal(t, http.StatusOK, invalidateResp.status,
		"invalidating upstream must queue its second round; body=%s", string(invalidateResp.raw))

	resumeResp := postJSON(t,
		h.ControlBase+fmt.Sprintf("/v1/instances/%s/resume", iid.String()),
		map[string]any{})
	require.Equal(t, http.StatusOK, resumeResp.status,
		"resume must succeed so the queued upstream round dispatches; body=%s", string(resumeResp.raw))

	awaited.Until(t, "the woken parked run "+parkedRunID+" to settle fresh", func() bool {
		var (
			found bool
			state string
		)
		h.QuerySQL(`SELECT state::text FROM rimsky_node_runs WHERE id = $1`,
			[]any{parkedRunID}, func(scan func(...any) error) error {
				found = true
				return scan(&state)
			})
		require.True(t, found,
			"the woken parked run %s was deleted: an upstream cascade during a park wakes the parked row "+
				"and queues a new round beside it; the most-recent mode's coalescing delete must skip the woken "+
				"row, so the parked unit of work still executes", parkedRunID)
		return state == string(cascade.NodeStateFresh)
	})

	h.WaitForDispatchCount(worker.ID, 2)
	h.WaitForAllRunsTerminal(worker.ID)

	var settled int
	h.QueryRowSQL(`SELECT COUNT(*) FROM rimsky_node_runs WHERE node_id = $1 AND state = 'fresh'`,
		[]any{worker.ID}, &settled)
	require.Equal(t, 2, settled,
		"both the woken parked row and the newly queued cascade round must settle fresh")
}
