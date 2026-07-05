// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
			}, scenario.WithSubscribes(node.SubscriptionEntry{Node: "a", Type: "terminal/success", ForceUpstreamRefresh: node.BoolPtr(false)})),
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
		var bLatestDbg *persistence.NodeRunLatest
		var bRowDbg *persistence.NodeRow
		_ = h.InTx(func(tx persistence.Tx) error {
			l, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, tx, b.ID)
			bLatestDbg = l
			if err != nil {
				return err
			}
			r, err := h.Persist.Nodes().Get(h.Ctx, b.ID, tx)
			bRowDbg = r
			return err
		})
		var stateStr string
		if bLatestDbg != nil {
			stateStr = string(bLatestDbg.State)
		}
		nodeType := ""
		if bRowDbg != nil {
			nodeType = bRowDbg.NodeType
		}
		t.Fatalf("b did not reach fresh — subscription without a when: predicate should have cascaded despite changed=false; b state=%v node_type=%v", stateStr, nodeType)
	}

	var aLatest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, tx, a.ID)
		aLatest = r
		return err
	}))
	require.NotNil(t, aLatest)
	require.NotNil(t, aLatest.SettlingSignalType)
	require.Equal(t, "terminal/success", *aLatest.SettlingSignalType,
		"successful executor terminal records settling_signal_type=terminal/success regardless of `changed`")
}

func TestNeverPropagateResolution_NewShape(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})
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

	var aLatest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, tx, a.ID)
		aLatest = r
		return err
	}))
	require.NotNil(t, aLatest)
	require.NotNil(t, aLatest.SettlingSignalType)
	require.Equal(t, "terminal/success", *aLatest.SettlingSignalType,
		"changed=true terminal records settling_signal_type=terminal/success")
}

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

	var aLatest, bLatest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		ra, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, tx, a.ID)
		if err != nil {
			return err
		}
		aLatest = ra
		rb, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, tx, b.ID)
		bLatest = rb
		return err
	}))
	require.NotNil(t, aLatest)
	require.NotNil(t, aLatest.SettlingSignalType)
	require.Equal(t, "terminal/success", *aLatest.SettlingSignalType,
		"changed=false terminal records settling_signal_type=terminal/success (changed-gate is receiver-side)")
	if bLatest != nil {
		require.Equal(t, cascade.NodeStateFresh, bLatest.State,
			"b should remain fresh on a no-op commit")
	}
	bID := b.ID
	require.Empty(t,
		eventwait.Events(h.Ctx, t, h.Persist, eventwait.Matcher{NodeID: &bID, Kind: "work_started", KindPrefix: "terminal/"}),
		"b must leave no dispatch/terminal events on the ledger — a changed=false terminal must not fire a when:payload.changed subscriber")
}

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
					"stub/fatal": {Action: "give_up"},
				},
			}),
			scenario.MakeNode(node.TemplateNodeDef{
				Type:     "b",
				Executor: "stub",
			}, scenario.WithSubscribes(node.SubscriptionEntry{Node: "a", Type: "terminal/success", ForceUpstreamRefresh: node.BoolPtr(false)})),
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

	var aLatest, bLatest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		ra, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, tx, a.ID)
		if err != nil {
			return err
		}
		aLatest = ra
		rb, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, tx, b.ID)
		bLatest = rb
		return err
	}))
	require.NotNil(t, aLatest)
	require.NotNil(t, aLatest.SettlingSignalType)
	require.Contains(t, *aLatest.SettlingSignalType, "terminal/error/",
		"give_up should record settling_signal_type=terminal/error/<class>")
	if bLatest != nil {
		require.NotEqual(t, cascade.NodeStateRunning, bLatest.State,
			"b should not run while upstream is failed")
	}
	bID := b.ID
	require.Empty(t,
		eventwait.Events(h.Ctx, t, h.Persist, eventwait.Matcher{NodeID: &bID, Kind: "work_started", KindPrefix: "terminal/"}),
		"b must leave no dispatch/terminal events on the ledger — a terminal/success subscriber must not fire on the upstream's terminal/error/<class>")
}

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
						Action: "pass",
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
	var wLatest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, tx, worker.ID)
		wLatest = r
		return err
	}))
	require.NotNil(t, wLatest)
	require.Equal(t, cascade.NodeStateFresh, wLatest.State, "worker should be fresh after pass")
}

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
						Action: "pass",
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
	var wLatest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, tx, worker.ID)
		wLatest = r
		return err
	}))
	require.NotNil(t, wLatest)
	require.Equal(t, cascade.NodeStateFresh, wLatest.State, "worker should be fresh after pass")
}

func waitForSettlingSignalType(t *testing.T, h *scenario.Harness, nodeID shared.UUID, want string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var latest *persistence.NodeRunLatest
		_ = h.InTx(func(tx persistence.Tx) error {
			r, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, tx, nodeID)
			latest = r
			return err
		})
		if latest != nil && latest.SettlingSignalType != nil && *latest.SettlingSignalType == want {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func waitForSettlingSignalTypePrefix(t *testing.T, h *scenario.Harness, nodeID shared.UUID, prefix string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var latest *persistence.NodeRunLatest
		_ = h.InTx(func(tx persistence.Tx) error {
			r, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, tx, nodeID)
			latest = r
			return err
		})
		if latest != nil && latest.SettlingSignalType != nil && strings.HasPrefix(*latest.SettlingSignalType, prefix) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

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
			}, scenario.WithSubscribes(node.SubscriptionEntry{Node: "a", Type: "terminal/*", ForceUpstreamRefresh: node.BoolPtr(false)})),
		},
	})
	iid := h.CreateInstance(tid, "ck-pure-cascade", map[string]any{})

	a := h.FindNode(iid, "a")
	p := h.FindNode(iid, "p")
	require.NotNil(t, a)
	require.NotNil(t, p)

	require.True(t, h.WaitForNodeState(p.ID, cascade.NodeStateFresh, 30*time.Second),
		"pure-cascade node p did not reach fresh")

	var pLatest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, tx, p.ID)
		pLatest = r
		return err
	}))
	require.NotNil(t, pLatest)
	require.NotNil(t, pLatest.SettlingSignalType)
	require.Equal(t, "terminal/success", *pLatest.SettlingSignalType,
		"pure-cascade transition should record settling_signal_type=terminal/success (carried from upstream)")
}
