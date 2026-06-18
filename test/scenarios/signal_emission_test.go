// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestSignalEmission_TerminalSuccess(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Success(map[string]any{"ok": true}, true, "ok-summary")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "signal-success", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "worker", Executor: "stub"},
				scenario.WithAttributes(map[string]any{
					"type":       "object",
					"properties": map[string]any{"ok": map[string]any{"type": "boolean"}},
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-signal-success", map[string]any{})
	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateFresh, 15*time.Second),
		"worker did not reach fresh")

	rows := readEventsForNode(t, h, n.ID)
	require.True(t, hasEventKind(rows, "terminal/success"),
		"expected one rimsky_events row with kind=terminal/success; got kinds=%v", kindsOf(rows))
	for _, e := range rows {
		if e.KindRaw == "terminal/success" {
			require.Equal(t, true, e.Payload["changed"],
				"terminal/success.payload.changed should be true (executor returned changed=true)")
			require.Equal(t, "ok-summary", e.Payload["change_summary"],
				"terminal/success.payload.change_summary should mirror executor's summary")
			break
		}
	}
}

func TestSignalEmission_TerminalErrorWithRetryThenGiveUp(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Error("foo", map[string]any{"why": "nope"})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "signal-retry-then-give-up", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:     "worker",
				Executor: "stub",
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"stub/foo": {Policy: []node.PolicyAction{
						{Action: "retry", Count: 1},
						{Action: "give_up"},
					}},
				},
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-signal-retry-give-up", map[string]any{})
	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateFailed, 30*time.Second),
		"worker should land in failed after retry-then-give-up")

	rows := readEventsForNode(t, h, n.ID)
	kinds := kindsOf(rows)
	retryIdx := indexOfKind(rows, "transient/retry/1/stub/foo")
	terminalIdx := indexOfKind(rows, "terminal/error/stub/foo")
	require.GreaterOrEqual(t, retryIdx, 0,
		"expected one transient/retry/1/stub/foo row; got kinds=%v", kinds)
	require.GreaterOrEqual(t, terminalIdx, 0,
		"expected one terminal/error/stub/foo row; got kinds=%v", kinds)
	require.Greater(t, retryIdx, terminalIdx,
		"transient/retry should precede terminal/error in time (DESC order: retryIdx > terminalIdx); kinds=%v", kinds)
}

func TestSignalEmission_ParkSnooze(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	resume := time.Now().Add(1 * time.Hour)
	h.Stub.WhenType("worker").Park(
		genv1.ParkReason_PARK_REASON_SNOOZE,
		"sleep-1h",
		resume,
	)

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "signal-park-snooze", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-signal-park-snooze", map[string]any{})
	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateParked, 30*time.Second),
		"worker did not reach parked")

	rows := readEventsForNode(t, h, n.ID)
	require.True(t, hasEventKind(rows, "terminal/park/snooze"),
		"expected one rimsky_events row with kind=terminal/park/snooze; got kinds=%v", kindsOf(rows))
	for _, e := range rows {
		if e.KindRaw != "terminal/park/snooze" {
			continue
		}
		require.NotNil(t, e.Payload["resume_at"], "park payload should carry resume_at")
		switch v := e.Payload["resume_at"].(type) {
		case string:
			require.NotEmpty(t, v, "resume_at string should be non-empty")
		case time.Time:
			require.False(t, v.IsZero(), "resume_at time should be non-zero")
		}
		break
	}
}

func readEventsForNode(t *testing.T, h *scenario.Harness, nodeID shared.UUID) []persistence.EventRow {
	t.Helper()
	nid := nodeID
	var res persistence.EventListResult
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Events().List(h.Ctx, persistence.EventListFilter{NodeID: &nid},
			persistence.ListPagination{Limit: 500}, tx)
		res = r
		return err
	}))
	return res.Events
}

func hasEventKind(rows []persistence.EventRow, kind string) bool {
	for _, e := range rows {
		if e.KindRaw == kind {
			return true
		}
	}
	return false
}

func indexOfKind(rows []persistence.EventRow, kind string) int {
	for i, e := range rows {
		if e.KindRaw == kind {
			return i
		}
	}
	return -1
}

func kindsOf(rows []persistence.EventRow) []string {
	out := make([]string, 0, len(rows))
	for _, e := range rows {
		out = append(out, e.KindRaw)
	}
	return out
}
