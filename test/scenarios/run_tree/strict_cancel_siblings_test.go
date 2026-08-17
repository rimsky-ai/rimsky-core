// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: fan-out
// @concept: cancel-siblings

package runtree

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func TestStrictAggregation_FailedChildCancelsInFlightSiblings(t *testing.T) {
	t.Parallel()
	h := startFanOutHarness(t)
	h.Stub.WhenType("fan-parent").
		AwaitAsyncCallback("strict-cancel-1", 5000).
		Then().AwaitAsyncCallback("strict-cancel-2", 5000).
		Then().AwaitAsyncCallback("strict-cancel-3", 5000)

	tid := fanOutTemplate(h, "run-tree-strict-cancels-siblings",
		tmplspec.AggregationPolicy{Kind: tmplspec.AggregationKindStrict})
	iid := h.CreateInstance(tid, "ck-run-tree-strict-cancel", map[string]any{})

	parent := h.FindNode(iid, "fan-parent")
	require.NotNil(t, parent)

	mainScopeID := h.GetLatestFrameRootRunScopeID(iid)

	for {
		var n int
		h.QueryRowSQL(`
			SELECT COUNT(*) FROM rimsky_node_runs r
			JOIN rimsky_run_scopes rs ON rs.id = r.run_scope_id
			WHERE rs.instance_id = $1 AND rs.partition_key <> '' AND r.state = 'running'`,
			[]any{iid}, &n)
		if n == 3 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	cbBase := "http://" + h.Supervisor.CallbackAddr()
	body, _ := json.Marshal(map[string]any{
		"error": map[string]any{
			"error_class": "partition_doom",
			"payload":     map[string]any{"why": "one partition failed"},
		},
	})
	postCallbackBodyRT(t, cbBase+"/v1/callback/strict-cancel-1", body)

	parentRunID := waitForParentMainRunState(t, h, parent.ID, mainScopeID.String(), cascade.NodeStateFailed)

	tree := readPersistedTree(t, h, iid)
	require.Equal(t, []string{"a", "b", "c"}, tree.PartitionKeys)
	for _, pk := range tree.PartitionKeys {
		require.Equal(t, parentRunID, tree.ParentRunIDs[pk],
			"child scope %q must link to the parent run", pk)
		require.Equal(t, string(cascade.NodeStateFailed), tree.ChildStates[pk],
			"partition %q must be failed: strict cancels every in-flight sibling when one child fails, "+
				"rather than leaving them running until their callback deadlines", pk)
	}

	cancelled := 0
	for _, pk := range tree.PartitionKeys {
		var sig string
		h.QueryRowSQL(`
			SELECT COALESCE(r.settling_signal_type, '') FROM rimsky_node_runs r
			 WHERE r.run_scope_id = $1
			 ORDER BY r.enqueued_at DESC LIMIT 1`,
			[]any{tree.ChildScopeIDs[pk]}, &sig)
		if sig == "terminal/error/sibling_failed" {
			cancelled++
		}
	}
	require.Equal(t, 2, cancelled,
		"the two partitions that did not fail on their own must carry the sibling_failed settling signal")

	var settlingSig string
	h.QueryRowSQL(`
		SELECT COALESCE(settling_signal_type, '') FROM rimsky_node_runs WHERE id = $1`,
		[]any{parentRunID}, &settlingSig)
	require.Equal(t, "terminal/error/aggregate/strict_failed", settlingSig)
}
