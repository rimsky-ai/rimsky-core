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
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/eventwait"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestTemplateErrorPolicy(t *testing.T) {
	t.Parallel()

	t.Run("pass_settles_fresh_and_cascades", testTemplateErrorPolicyPass)
	t.Run("give_up_terminates_node_skips_downstream", testTemplateErrorPolicyGiveUp)
	t.Run("retry_re_dispatches", testTemplateErrorPolicyRetry)
}

func testTemplateErrorPolicyPass(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("worker").Error("boom_pass", map[string]any{"why": "pass-branch"})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "tmpl-error-policy-pass", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:     "worker",
				Executor: "stub",
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"stub/boom_pass": {
						Action: "pass",
					},
				},
			}),
			scenario.MakeNode(node.TemplateNodeDef{Type: "downstream"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "worker", Type: "terminal/*",
					WakeOnChange:         node.BoolPtr(true),
					ForceUpstreamRefresh: node.BoolPtr(false),
				})),
		},
	})
	iid := h.CreateInstance(tid, "ck-tmpl-err-pass", map[string]any{})

	worker := h.FindNode(iid, "worker")
	downstream := h.FindNode(iid, "downstream")
	require.NotNil(t, worker)
	require.NotNil(t, downstream)

	require.True(t, waitForSettlingSignalTypePrefix(t, h, worker.ID, "terminal/error/", 30*time.Second),
		"worker should record settling_signal_type=terminal/error/<class> under pass")

	var workerLatest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, tx, worker.ID)
		workerLatest = r
		return err
	}))
	require.NotNil(t, workerLatest)
	require.Equal(t, cascade.NodeStateFresh, workerLatest.State,
		"pass must settle the node fresh, not failed — the action declaration is what differentiates pass from give_up")

	require.True(t, h.WaitForNodeState(downstream.ID, cascade.NodeStateFresh, 30*time.Second),
		"pass must continue the cascade — a downstream subscriber on terminal/* must fire on the worker's terminal/error/<class> signal")

	dispatchCount := 0
	for _, o := range h.Stub.Observed() {
		if o.NodeType == "worker" {
			dispatchCount++
		}
	}
	require.Equal(t, 1, dispatchCount,
		"pass must NOT re-dispatch — exactly one worker dispatch expected; multiple would mean the runtime treated pass as retry")
}

func testTemplateErrorPolicyGiveUp(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("worker").Error("boom_giveup", map[string]any{"why": "give-up-branch"})
	h.Stub.WhenType("downstream").Success(map[string]any{}, true, "must-not-run")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "tmpl-error-policy-giveup", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:     "worker",
				Executor: "stub",
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"stub/boom_giveup": {
						Action: "give_up",
					},
				},
			}),
			scenario.MakeNode(node.TemplateNodeDef{
				Type:     "downstream",
				Executor: "stub",
			},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "worker", Type: "terminal/success",
					WakeOnChange:         node.BoolPtr(true),
					ForceUpstreamRefresh: node.BoolPtr(false),
				})),
		},
	})
	iid := h.CreateInstance(tid, "ck-tmpl-err-giveup", map[string]any{})

	worker := h.FindNode(iid, "worker")
	downstream := h.FindNode(iid, "downstream")
	require.NotNil(t, worker)
	require.NotNil(t, downstream)

	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateFailed, 30*time.Second),
		"give_up must drive the worker to state=failed; pass would settle fresh and retry would not settle at all")

	time.Sleep(2 * time.Second)

	var downstreamLatest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, tx, downstream.ID)
		downstreamLatest = r
		return err
	}))
	if downstreamLatest != nil {
		require.Equal(t, cascade.NodeStateFresh, downstreamLatest.State,
			"give_up must skip downstream — the downstream subscribes on terminal/success and the worker's give_up emits terminal/error/<class>, which must not cascade to it")
	}

	dsID := downstream.ID
	require.Empty(t,
		eventwait.Events(h.Ctx, t, h.Persist, eventwait.Matcher{NodeID: &dsID, Kind: "work_started", KindPrefix: "terminal/"}),
		"downstream must leave no dispatch/terminal events on the ledger when give_up fires upstream")

	for _, o := range h.Stub.Observed() {
		require.NotEqual(t, "downstream", o.NodeType,
			"downstream executor must not be invoked when give_up fires on the upstream worker")
	}

	var workerLatest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, tx, worker.ID)
		workerLatest = r
		return err
	}))
	require.NotNil(t, workerLatest)
	require.NotNil(t, workerLatest.SettlingSignalType)
	require.Contains(t, *workerLatest.SettlingSignalType, "terminal/error/",
		"give_up must record settling_signal_type=terminal/error/<class>")
}

func testTemplateErrorPolicyRetry(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("worker").Error("boom_retry", map[string]any{"why": "retry-branch"})

	const retryCount = 3
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "tmpl-error-policy-retry", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(node.TemplateNodeDef{
				Type:         "worker",
				Executor:     "stub",
				MaxRetries:   node.IntPtr(retryCount),
				RetryBackoff: &node.RetryBackoffConfig{BaseDelayMs: 50},
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"stub/boom_retry": {Action: "retry"},
				},
			}),
		},
	})
	iid := h.CreateInstance(tid, "ck-tmpl-err-retry", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateFailed, 60*time.Second),
		"retry chain must run to exhaustion then fall through to give_up; reaching failed proves both the retry dispatches happened and the chain advanced past the retry slot")

	dispatchCount := 0
	for _, o := range h.Stub.Observed() {
		if o.NodeType == "worker" {
			dispatchCount++
		}
	}
	require.GreaterOrEqual(t, dispatchCount, retryCount+1,
		"retry must produce at least %d worker dispatches (initial + %d retries); got %d — the runtime did not re-dispatch on retry",
		retryCount+1, retryCount, dispatchCount)

	var retryEventCount int
	h.QueryRowSQL(
		`SELECT count(*) FROM rimsky_events WHERE node_id = $1 AND kind LIKE 'transient/retry/%'`,
		[]any{worker.ID},
		&retryEventCount,
	)
	require.GreaterOrEqual(t, retryEventCount, retryCount,
		"each retry must emit a transient/retry/<n>/<class> audit row; expected at least %d, got %d",
		retryCount, retryEventCount)

}

