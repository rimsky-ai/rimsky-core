// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: fan-out
// @concept: cancel-siblings
// @concept: node-run

package runtree

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func postCallbackBodyRT(t *testing.T, url string, body []byte) {
	t.Helper()
	for {
		resp, err := http.Post(url, "application/json", bytes.NewReader(body))
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		_, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestErrorPolicyFirst_WinnerCancelsInFlightLosers(t *testing.T) {
	t.Parallel()
	h := startFanOutHarness(t)
	h.Stub.WhenType("fan-parent").
		AwaitAsyncCallback("first-win-1", 5000).
		Then().AwaitAsyncCallback("first-win-2", 5000).
		Then().AwaitAsyncCallback("first-win-3", 5000)

	tid := fanOutTemplate(h, "run-tree-first-cancels-losers",
		tmplspec.AggregationPolicy{Kind: tmplspec.AggregationKindFirst})
	iid := h.CreateInstance(tid, "ck-run-tree-first-cancel", map[string]any{})

	parent := h.FindNode(iid, "fan-parent")
	require.NotNil(t, parent)

	mainScopeID := h.GetMainRunScopeID(iid)

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
		"success": map[string]any{
			"attributes_delta": map[string]any{"ok": true},
			"changed":          true,
			"change_summary":   "winner",
		},
	})
	postCallbackBodyRT(t, cbBase+"/v1/callback/first-win-1", body)

	parentRunID := waitForParentMainRunState(t, h, parent.ID, mainScopeID.String(), cascade.NodeStateFresh)

	tree := readPersistedTree(t, h, iid)
	require.Equal(t, []string{"a", "b", "c"}, tree.PartitionKeys)

	freshCount, failedCount := 0, 0
	var cancelledScopeIDs []string
	for _, pk := range tree.PartitionKeys {
		require.Equal(t, parentRunID, tree.ParentRunIDs[pk],
			"child scope %q must link to the parent run", pk)
		switch tree.ChildStates[pk] {
		case string(cascade.NodeStateFresh):
			freshCount++
		case string(cascade.NodeStateFailed):
			failedCount++
			cancelledScopeIDs = append(cancelledScopeIDs, tree.ChildScopeIDs[pk])
		}
	}
	require.Equal(t, 1, freshCount,
		"exactly one partition must win under first-commit-wins")
	require.Equal(t, 2, failedCount,
		"the remaining in-flight partitions must be force-cancelled by the winner's commit, not left running forever")

	for _, scopeID := range cancelledScopeIDs {
		var sig string
		h.QueryRowSQL(`
			SELECT COALESCE(r.settling_signal_type, '') FROM rimsky_node_runs r
			 WHERE r.run_scope_id = $1
			 ORDER BY r.enqueued_at DESC LIMIT 1`,
			[]any{scopeID}, &sig)
		require.Equal(t, "terminal/error/sibling_failed", sig,
			"a run cancelled by the first-policy winner must carry the sibling_failed settling signal")
	}
}
