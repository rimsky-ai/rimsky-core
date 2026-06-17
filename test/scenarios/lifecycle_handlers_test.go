// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Lifecycle-handler scenario tests reshaped 2026-05-23 per spec
// .ok-planner/specs/2026-05-23-signal-taxonomy-and-policy-decoupling-design.md.
//
// The three declarative slots (`on_acquire_unavailable`,
// `on_executor_complete`, `on_executor_errored`) retired alongside
// `concept:lifecycle-handler`. Their behaviors are now expressed:
//   - on_acquire_unavailable → `error_types: { "acquire/unavailable":
//     { policy: [...] } }`.
//   - on_executor_complete (always_propagate / never_propagate) →
//     receiver-side CEL `when: payload.changed` (or omitting it for
//     always-fire) on `terminal/success` subscriptions.
//   - on_executor_errored (pass) → `error_types: { <class>: { policy:
//     [{action: pass}] } }`.
//
// Tests below exercise the new shapes.
package scenarios

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/eventwait"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// TestAlwaysPropagateResolution_NewShape (was Task 31). Post-2026-05-23
// the on_executor_complete.always_propagate resolve retires; a
// subscriber that wants to fire regardless of payload.changed simply
// omits the `when:` predicate on its `terminal/success` subscription.
// Test: a Complete{changed:false} terminal still fires the subscriber
// because the subscriber has no `when:` filter (default = always).
func TestAlwaysPropagateResolution_NewShape(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("a").Success(map[string]any{"a": 1}, false, "noop")
	h.Stub.WhenType("b").Success(map[string]any{"b": 1}, true, "b")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "always-propagate", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:     "a",
				Executor: "stub",
			}),
			scenario.MakeNode(node.TemplateNodeDef{
				Type:     "b",
				Executor: "stub",
				// @deliberate: No `when:` → fire on every terminal/success, regardless
				// of payload.changed. This replaces the pre-reshape
				// `on_executor_complete: { resolve: always_propagate }`.
			}, scenario.WithSubscribes(node.SubscriptionEntry{Node: "a", Type: "terminal/success", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)})),
		},
	})
	iid := h.CreateInstance(tid, "ck-always", map[string]any{})

	a := h.FindNode(iid, "a")
	b := h.FindNode(iid, "b")
	require.NotNil(t, a)
	require.NotNil(t, b)

	require.True(t, h.WaitForNodeState(a.ID, cascade.NodeStateFresh, 30*time.Second),
		"a did not reach fresh")
	if !h.WaitForNodeState(b.ID, cascade.NodeStateFresh, 30*time.Second) {
		var bRowDbg *persistence.NodeRow
		_ = h.InTx(func(tx persistence.Tx) error {
			r, err := h.Persist.Nodes().Get(h.Ctx, b.ID, tx)
			bRowDbg = r
			return err
		})
		t.Fatalf("b did not reach fresh — subscription without a when: predicate should have cascaded despite changed=false; b state=%v frame_id=%v", bRowDbg.State, bRowDbg.FrameID)
	}

	// @deliberate: Verify a's settling_signal_type — under the canonical signal
	// taxonomy a successful executor terminal records
	// settling_signal_type=terminal/success regardless of the
	// `changed` flag (selectivity is receiver-side via CEL
	// `when: payload.changed`, not sender-side).
	var aRow *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().Get(h.Ctx, a.ID, tx)
		aRow = r
		return err
	}))
	require.NotNil(t, aRow.SettlingSignalType)
	require.Equal(t, "terminal/success", *aRow.SettlingSignalType,
		"successful executor terminal records settling_signal_type=terminal/success regardless of `changed`")
}

// TestNeverPropagateResolution_NewShape (was Task 32). Post-2026-05-23
// the on_executor_complete.never_propagate resolve retires; a
// subscriber that wants to fire only on payload.changed declares
// `when: payload.changed`. Test: a Complete{changed:true} terminal
// fires the subscriber because the CEL when: evaluates true; the
// inverse (Complete{changed:false}) does NOT fire (see
// TestFreshUnchangedDoesNotCascade for that path).
func TestNeverPropagateResolution_NewShape(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	// @constraint: Script a-changed=true so the changed-gate subscriber DOES fire.
	// The legacy "never_propagate" semantic (a-changed=true that does
	// NOT cascade) requires the subscriber to omit `terminal/success`
	// entirely — that's documented in concept:node-subscription, not
	// exercised here.
	h.Stub.WhenType("a").Success(map[string]any{"a": 1}, true, "a-changed")
	h.Stub.WhenType("b").Success(map[string]any{"b": 1}, true, "b")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "changed-gate", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:     "a",
				Executor: "stub",
			}),
			scenario.MakeNode(node.TemplateNodeDef{
				Type:     "b",
				Executor: "stub",
			}, scenario.WithSubscribes(node.SubscriptionEntry{
				Node:                 "a",
				Type:                 "terminal/success",
				When:                 "payload.changed",
				WakeOnChange:         node.BoolPtr(true),
				ForceUpstreamRefresh: node.BoolPtr(false),
			})),
		},
	})
	iid := h.CreateInstance(tid, "ck-changed-gate", map[string]any{})

	a := h.FindNode(iid, "a")
	b := h.FindNode(iid, "b")
	require.NotNil(t, a)
	require.NotNil(t, b)

	require.True(t, h.WaitForNodeState(a.ID, cascade.NodeStateFresh, 30*time.Second),
		"a did not reach fresh")
	require.True(t, h.WaitForNodeState(b.ID, cascade.NodeStateFresh, 30*time.Second),
		"b did not reach fresh — when: payload.changed should fire on changed=true")

	var aRow *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().Get(h.Ctx, a.ID, tx)
		aRow = r
		return err
	}))
	require.NotNil(t, aRow.SettlingSignalType)
	require.Equal(t, "terminal/success", *aRow.SettlingSignalType,
		"changed=true terminal records settling_signal_type=terminal/success")
}

// TestFreshUnchangedDoesNotCascade: a changed=false terminal must NOT
// fire a downstream subscriber gated on `when: payload.changed`.
// (Post-2026-05-23 taxonomy there is no sender-side by_changed default —
// the changed-gate is declared receiver-side via CEL; an ungated
// `terminal/*` edge fires on every terminal regardless of `changed`,
// which `cascade_signal_blind_e2e_test.go` pins. The 2026-06-11 polling
// audit's durable-record check exposed that this test's previous
// ungated edge made its no-cascade premise vacuous: b ran on a's
// changed=false terminal and the fresh-state sample couldn't see it.)
func TestFreshUnchangedDoesNotCascade(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("a").Success(map[string]any{"a": 1}, false, "noop")
	h.Stub.WhenType("b").Success(map[string]any{"b": 1}, true, "b")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "fresh-unchanged-no-cascade", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "a", Executor: "stub"}),
			scenario.MakeNode(node.TemplateNodeDef{
				Type:     "b",
				Executor: "stub",
			}, scenario.WithSubscribes(node.SubscriptionEntry{
				Node: "a", Type: "terminal/*", When: "payload.changed",
				WakeOnChange:         node.BoolPtr(true),
				ForceUpstreamRefresh: node.BoolPtr(false),
			})),
		},
	})
	iid := h.CreateInstance(tid, "ck-fucnc", map[string]any{})

	a := h.FindNode(iid, "a")
	b := h.FindNode(iid, "b")
	require.NotNil(t, a)
	require.NotNil(t, b)

	require.True(t, h.WaitForNodeState(a.ID, cascade.NodeStateFresh, 30*time.Second),
		"a did not reach fresh")
	time.Sleep(2 * time.Second)

	var aRow, bRow *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		ra, err := h.Persist.Nodes().Get(h.Ctx, a.ID, tx)
		if err != nil {
			return err
		}
		aRow = ra
		rb, err := h.Persist.Nodes().Get(h.Ctx, b.ID, tx)
		bRow = rb
		return err
	}))
	require.NotNil(t, aRow.SettlingSignalType)
	require.Equal(t, "terminal/success", *aRow.SettlingSignalType,
		"changed=false terminal records settling_signal_type=terminal/success (changed-gate is receiver-side)")
	require.Equal(t, cascade.NodeStateFresh, bRow.State,
		"b should remain fresh on a no-op commit")
	// @constraint: Durable-record check (2026-06-11 polling audit): the fresh-state
	// sample above cannot distinguish "b never ran" from "b spuriously
	// ran and settled back to fresh during the grace window". The
	// append-only event log can: a dispatched b leaves work_started and
	// terminal/* rows that no later transition erases.
	bID := b.ID
	require.Empty(t,
		eventwait.Events(h.Ctx, t, h.Persist, eventwait.Matcher{NodeID: &bID, Kind: "work_started", KindPrefix: "terminal/"}),
		"b must leave no dispatch/terminal events on the ledger — a changed=false terminal must not fire a when:payload.changed subscriber")
}

// TestFailedUpstreamFreezesDownstream: a failed upstream freezes a
// downstream that subscribes to `terminal/success` — the give_up's
// terminal/error/<class> envelope is structurally disjoint from the
// subscription's type-path, so the cascade never fires it. (Per
// concept:cascade, freeze-on-error is expressed receiver-side by NOT
// subscribing to terminal/error/*; a `terminal/*` edge — this test's
// previous shape — matches error envelopes too and fires, which the
// 2026-06-11 polling audit's durable-record check exposed: b ran on
// a's failure and the state sample couldn't see it.)
func TestFailedUpstreamFreezesDownstream(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("a").Error("fatal", map[string]any{"why": "boom"})
	h.Stub.WhenType("b").Success(map[string]any{"b": 1}, true, "b")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "failed-freezes", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:     "a",
				Executor: "stub",
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"stub/fatal": {Policy: []node.PolicyAction{{Action: "give_up"}}},
				},
			}),
			scenario.MakeNode(node.TemplateNodeDef{
				Type:     "b",
				Executor: "stub",
			}, scenario.WithSubscribes(node.SubscriptionEntry{Node: "a", Type: "terminal/success", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)})),
		},
	})
	iid := h.CreateInstance(tid, "ck-fail-freeze", map[string]any{})

	a := h.FindNode(iid, "a")
	b := h.FindNode(iid, "b")
	require.NotNil(t, a)
	require.NotNil(t, b)

	require.True(t, h.WaitForNodeState(a.ID, cascade.NodeStateFailed, 30*time.Second),
		"a should land in failed")
	time.Sleep(2 * time.Second)

	var aRow, bRow *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		ra, err := h.Persist.Nodes().Get(h.Ctx, a.ID, tx)
		if err != nil {
			return err
		}
		aRow = ra
		rb, err := h.Persist.Nodes().Get(h.Ctx, b.ID, tx)
		bRow = rb
		return err
	}))
	require.NotNil(t, aRow.SettlingSignalType)
	require.Contains(t, *aRow.SettlingSignalType, "terminal/error/",
		"give_up should record settling_signal_type=terminal/error/<class>")
	require.NotEqual(t, cascade.NodeStateRunning, bRow.State,
		"b should not run while upstream is failed")
	// @deliberate: Durable-record check (2026-06-11 polling audit): sampling
	// bRow.State for "not running" can trivially miss a transient run —
	// b could run and settle between the grace window and the read. The
	// event log is append-only: any dispatch of b leaves work_started /
	// terminal/* rows.
	bID := b.ID
	require.Empty(t,
		eventwait.Events(h.Ctx, t, h.Persist, eventwait.Matcher{NodeID: &bID, Kind: "work_started", KindPrefix: "terminal/"}),
		"b must leave no dispatch/terminal events on the ledger — a terminal/success subscriber must not fire on the upstream's terminal/error/<class>")
}

// TestExecutorBlockedPassResolution_NewShape (was Task 37). Post-2026-
// 05-23 the on_executor_errored.pass resolve retires; the replacement
// is a per-class `pass` action in `error_types:`. A stub-emitted
// Error{executor_blocked} routed through error_types: { executor_blocked:
// { policy: [pass] } } lands the node in fresh+passed.
func TestExecutorBlockedPassResolution_NewShape(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Error("executor_blocked", map[string]any{
		"reason": "blocked_class",
		"why":    "stub-blocked",
	})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "blocked-pass", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:     "worker",
				Executor: "stub",
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"stub/executor_blocked": {
						Policy: []node.PolicyAction{{Action: "pass"}},
					},
				},
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-blocked-pass", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	require.True(t, waitForSettlingSignalTypePrefix(t, h, worker.ID, "terminal/error/", 30*time.Second),
		"worker should record settling_signal_type=terminal/error/<class> under error_types: { executor_blocked: pass }")
	var wRow *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().Get(h.Ctx, worker.ID, tx)
		wRow = r
		return err
	}))
	require.Equal(t, cascade.NodeStateFresh, wRow.State, "worker should be fresh after pass")
}

// TestExecutorErroredPassResolution_NewShape (was Task 38). Same as
// TestExecutorBlockedPassResolution_NewShape but for an arbitrary
// error_class.
func TestExecutorErroredPassResolution_NewShape(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("worker").Error("any_class", map[string]any{"why": "stub-err"})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "errored-pass", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:     "worker",
				Executor: "stub",
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"stub/any_class": {
						Policy: []node.PolicyAction{{Action: "pass"}},
					},
				},
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-errored-pass", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	require.True(t, waitForSettlingSignalTypePrefix(t, h, worker.ID, "terminal/error/", 30*time.Second),
		"worker should record settling_signal_type=terminal/error/<class> under error_types: { any_class: pass }")
	var wRow *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().Get(h.Ctx, worker.ID, tx)
		wRow = r
		return err
	}))
	require.Equal(t, cascade.NodeStateFresh, wRow.State, "worker should be fresh after pass")
}

// waitForSettlingSignalType polls the node row until settling_signal_type
// matches exactly. Replaces the retired waitForLastOutcome per Pass 5 of
// spec 2026-05-23-signal-taxonomy-and-policy-decoupling-design.
func waitForSettlingSignalType(t *testing.T, h *scenario.Harness, nodeID shared.UUID, want string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var row *persistence.NodeRow
		_ = h.InTx(func(tx persistence.Tx) error {
			r, err := h.Persist.Nodes().Get(h.Ctx, nodeID, tx)
			row = r
			return err
		})
		if row != nil && row.SettlingSignalType != nil && *row.SettlingSignalType == want {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// waitForSettlingSignalTypePrefix polls until settling_signal_type
// starts with the given prefix (e.g. "terminal/error/" matches any
// error class). Used to assert on the pass / give_up resolution class
// without binding to the specific error class.
func waitForSettlingSignalTypePrefix(t *testing.T, h *scenario.Harness, nodeID shared.UUID, prefix string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var row *persistence.NodeRow
		_ = h.InTx(func(tx persistence.Tx) error {
			r, err := h.Persist.Nodes().Get(h.Ctx, nodeID, tx)
			row = r
			return err
		})
		if row != nil && row.SettlingSignalType != nil && strings.HasPrefix(*row.SettlingSignalType, prefix) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// TestPureCascadeOutcomeColumn covers Task 33 (subset). A pure-cascade
// transition (stale → fresh via ReasonPureCascade) must record
// last_outcome=pure_cascade.
func TestPureCascadeOutcomeColumn(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
	h.Stub.WhenType("a").Success(map[string]any{"a": 1}, true, "a")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "pure-cascade", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{Type: "a", Executor: "stub"}),
			scenario.MakeNode(node.TemplateNodeDef{
				Type: "p",
			}, scenario.WithSubscribes(node.SubscriptionEntry{Node: "a", Type: "terminal/*", WakeOnChange: node.BoolPtr(true), ForceUpstreamRefresh: node.BoolPtr(false)})),
		},
	})
	iid := h.CreateInstance(tid, "ck-pure-cascade", map[string]any{})

	a := h.FindNode(iid, "a")
	p := h.FindNode(iid, "p")
	require.NotNil(t, a)
	require.NotNil(t, p)

	require.True(t, h.WaitForNodeState(p.ID, cascade.NodeStateFresh, 30*time.Second),
		"pure-cascade node p did not reach fresh")

	var pRow *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().Get(h.Ctx, p.ID, tx)
		pRow = r
		return err
	}))
	require.NotNil(t, pRow.SettlingSignalType)
	require.Equal(t, "terminal/success", *pRow.SettlingSignalType,
		"pure-cascade transition should record settling_signal_type=terminal/success (carried from upstream)")
}
