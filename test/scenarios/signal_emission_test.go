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
	h.WaitForNodeState(n.ID, cascade.NodeStateFresh)

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
				Type:       "worker",
				Executor:   "stub",
				MaxRetries: node.IntPtr(1),
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"stub/foo": {Action: "retry"},
				},
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-signal-retry-give-up", map[string]any{})
	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	h.WaitForNodeState(n.ID, cascade.NodeStateFailed)

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

func TestSignalEmission_Park(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	resume := time.Now().Add(1 * time.Hour)
	h.Stub.WhenType("worker").Park(resume)

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "signal-park-snooze", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-signal-park-snooze", map[string]any{})
	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	h.WaitForNodeState(n.ID, cascade.NodeStateParked)

	rows := readEventsForNode(t, h, n.ID)
	require.True(t, hasEventKind(rows, "transient/park"),
		"expected one rimsky_events row with kind=transient/park; got kinds=%v", kindsOf(rows))
	for _, e := range rows {
		if e.KindRaw != "transient/park" {
			continue
		}
		require.NotNil(t, e.Payload["resume_at"], "park payload should carry resume_at")
		switch v := e.Payload["resume_at"].(type) {
		case string:
			require.NotEmpty(t, v, "resume_at string should be non-empty")
		case time.Time:
			require.False(t, v.IsZero(), "resume_at time should be non-zero")
		default:
			t.Fatalf("resume_at has unexpected type %T (value %v); want string or time.Time", v, v)
		}
		break
	}
}

func TestSignalEmission_TerminalErrorCarriesAttributesDelta(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").
		Error("foo", map[string]any{"why": "nope"}).
		AttributesDelta(map[string]any{"retry_count": 3.0, "last_class": "stub/foo"})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "signal-error-attrs-delta", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:     "worker",
				Executor: "stub",
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-signal-error-attrs-delta", map[string]any{})
	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	h.WaitForNodeState(n.ID, cascade.NodeStateFailed)

	rows := readEventsForNode(t, h, n.ID)
	require.True(t, hasEventKind(rows, "terminal/error/stub/foo"),
		"expected terminal/error/stub/foo row; got kinds=%v", kindsOf(rows))
	for _, e := range rows {
		if e.KindRaw != "terminal/error/stub/foo" {
			continue
		}
		delta, ok := e.Payload["attributes_delta"].(map[string]any)
		require.True(t, ok,
			"terminal/error.payload.attributes_delta should be a map; got %T (%+v)",
			e.Payload["attributes_delta"], e.Payload)
		require.Equal(t, "stub/foo", delta["last_class"],
			"executor-supplied attributes_delta should ride the terminal/error signal payload")
		require.Equal(t, 3.0, delta["retry_count"],
			"executor-supplied attributes_delta should ride the terminal/error signal payload")
		break
	}
}

func TestSignalEmission_TransientParkAuditOnlyNoAttributesDelta(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	resume := time.Now().Add(1 * time.Hour)
	h.Stub.WhenType("worker").
		Park(resume).
		AttributesDelta(map[string]any{"session_token": "abc-123"})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "signal-park-no-attrs-delta", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "worker", Executor: "stub"}),
		},
	})
	iid := h.CreateInstance(tid, "ck-signal-park-no-attrs-delta", map[string]any{})
	n := h.FindNode(iid, "worker")
	require.NotNil(t, n)
	h.WaitForNodeState(n.ID, cascade.NodeStateParked)

	rows := readEventsForNode(t, h, n.ID)
	require.True(t, hasEventKind(rows, "transient/park"),
		"expected transient/park audit row; got kinds=%v", kindsOf(rows))
	for _, e := range rows {
		if e.KindRaw != "transient/park" {
			continue
		}
		_, hasDelta := e.Payload["attributes_delta"]
		require.False(t, hasDelta,
			"transient/park signal payload must NOT carry attributes_delta — park is audit-only and the delta is merged to the per-run row directly; payload=%+v",
			e.Payload)
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
