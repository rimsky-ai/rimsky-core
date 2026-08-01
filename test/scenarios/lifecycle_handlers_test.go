// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

	h.WaitForNodeState(a.ID, cascade.NodeStateFresh)
	h.WaitForNodeState(b.ID, cascade.NodeStateFresh)

	var aLatest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, a.ID, tx)
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

	h.WaitForNodeState(a.ID, cascade.NodeStateFresh)
	h.WaitForNodeState(b.ID, cascade.NodeStateFresh)

	var aLatest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, a.ID, tx)
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

	h.WaitForNodeState(a.ID, cascade.NodeStateFresh)

	bID := b.ID
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		require.NoError(t, h.InTx(func(tx persistence.Tx) error {
			rb, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, b.ID, tx)
			if err != nil {
				return err
			}
			if rb != nil {
				require.Equal(t, cascade.NodeStateFresh, rb.State,
					"b should remain fresh on a no-op commit")
			}
			return nil
		}))
		require.Empty(t,
			eventwait.Events(h.Ctx, t, h.Persist, eventwait.Matcher{NodeID: &bID, Kind: "work_started", KindPrefix: "terminal/"}),
			"b must leave no dispatch/terminal events on the ledger — a changed=false terminal must not fire a when:payload.changed subscriber")
		time.Sleep(50 * time.Millisecond)
	}

	var aLatest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		ra, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, a.ID, tx)
		aLatest = ra
		return err
	}))
	require.NotNil(t, aLatest)
	require.NotNil(t, aLatest.SettlingSignalType)
	require.Equal(t, "terminal/success", *aLatest.SettlingSignalType,
		"changed=false terminal records settling_signal_type=terminal/success (changed-gate is receiver-side)")
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

	h.WaitForNodeState(a.ID, cascade.NodeStateFailed)

	bID := b.ID
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		require.NoError(t, h.InTx(func(tx persistence.Tx) error {
			rb, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, b.ID, tx)
			if err != nil {
				return err
			}
			if rb != nil {
				require.NotEqual(t, cascade.NodeStateRunning, rb.State,
					"b should not run while upstream is failed")
			}
			return nil
		}))
		require.Empty(t,
			eventwait.Events(h.Ctx, t, h.Persist, eventwait.Matcher{NodeID: &bID, Kind: "work_started", KindPrefix: "terminal/"}),
			"b must leave no dispatch/terminal events on the ledger — a terminal/success subscriber must not fire on the upstream's terminal/error/<class>")
		time.Sleep(50 * time.Millisecond)
	}

	var aLatest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		ra, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, a.ID, tx)
		aLatest = ra
		return err
	}))
	require.NotNil(t, aLatest)
	require.NotNil(t, aLatest.SettlingSignalType)
	require.Contains(t, *aLatest.SettlingSignalType, "terminal/error/",
		"give_up should record settling_signal_type=terminal/error/<class>")
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

	waitForSettlingSignalTypePrefix(t, h, worker.ID, "terminal/error/")
	var wLatest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, worker.ID, tx)
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

	waitForSettlingSignalTypePrefix(t, h, worker.ID, "terminal/error/")
	var wLatest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, worker.ID, tx)
		wLatest = r
		return err
	}))
	require.NotNil(t, wLatest)
	require.Equal(t, cascade.NodeStateFresh, wLatest.State, "worker should be fresh after pass")
}

func waitForSettlingSignalType(t *testing.T, h *scenario.Harness, nodeID shared.UUID, want string) {
	t.Helper()
	for {
		var latest *persistence.NodeRunLatest
		_ = h.InTx(func(tx persistence.Tx) error {
			r, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, nodeID, tx)
			latest = r
			return err
		})
		if latest != nil && latest.SettlingSignalType != nil && *latest.SettlingSignalType == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func waitForSettlingSignalTypePrefix(t *testing.T, h *scenario.Harness, nodeID shared.UUID, prefix string) {
	t.Helper()
	for {
		var latest *persistence.NodeRunLatest
		_ = h.InTx(func(tx persistence.Tx) error {
			r, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, nodeID, tx)
			latest = r
			return err
		})
		if latest != nil && latest.SettlingSignalType != nil && strings.HasPrefix(*latest.SettlingSignalType, prefix) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
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

	h.WaitForNodeState(p.ID, cascade.NodeStateFresh)

	var pLatest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, p.ID, tx)
		pLatest = r
		return err
	}))
	require.NotNil(t, pLatest)
	require.NotNil(t, pLatest.SettlingSignalType)
	require.Equal(t, "terminal/success", *pLatest.SettlingSignalType,
		"pure-cascade transition should record settling_signal_type=terminal/success (carried from upstream)")
}
