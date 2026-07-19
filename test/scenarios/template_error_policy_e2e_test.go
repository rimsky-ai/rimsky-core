// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/claim_producers/stub/testfixture"
	"github.com/rimsky-ai/rimsky-core/test/support/eventwait"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestTemplateErrorPolicy(t *testing.T) {
	t.Parallel()

	t.Run("pass_settles_fresh_and_cascades", testTemplateErrorPolicyPass)
	t.Run("give_up_terminates_node_skips_downstream", testTemplateErrorPolicyGiveUp)
	t.Run("retry_re_dispatches", testTemplateErrorPolicyRetry)
	t.Run("release_and_requeue_abandons_claim_and_re_acquires", testTemplateErrorPolicyReleaseAndRequeue)
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
					ForceUpstreamRefresh: node.BoolPtr(false),
				})),
		},
	})
	iid := h.CreateInstance(tid, "ck-tmpl-err-pass", map[string]any{})

	worker := h.FindNode(iid, "worker")
	downstream := h.FindNode(iid, "downstream")
	require.NotNil(t, worker)
	require.NotNil(t, downstream)

	waitForSettlingSignalTypePrefix(t, h, worker.ID, "terminal/error/")

	var workerLatest *persistence.NodeRunLatest
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().GetLatestRunForNode(h.Ctx, tx, worker.ID)
		workerLatest = r
		return err
	}))
	require.NotNil(t, workerLatest)
	require.Equal(t, cascade.NodeStateFresh, workerLatest.State,
		"pass must settle the node fresh, not failed — the action declaration is what differentiates pass from give_up")

	h.WaitForNodeState(downstream.ID, cascade.NodeStateFresh)

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
					ForceUpstreamRefresh: node.BoolPtr(false),
				})),
		},
	})
	iid := h.CreateInstance(tid, "ck-tmpl-err-giveup", map[string]any{})

	worker := h.FindNode(iid, "worker")
	downstream := h.FindNode(iid, "downstream")
	require.NotNil(t, worker)
	require.NotNil(t, downstream)

	h.WaitForNodeState(worker.ID, cascade.NodeStateFailed)

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

	h.WaitForNodeState(worker.ID, cascade.NodeStateFailed)

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

func testTemplateErrorPolicyReleaseAndRequeue(t *testing.T) {
	t.Parallel()

	endpoint, sub, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
		PickPolicies: map[string]stubstore.PickPolicyConfig{
			"@queue": {
				OnCommit:     action.Action{Kind: action.Pop},
				OnGiveUp:     action.Action{Kind: action.Recycle},
				InitialItems: []json.RawMessage{json.RawMessage(`{"k":"v"}`)},
			},
		},
	})
	t.Cleanup(teardown)

	h := scenario.Start(t, scenario.HarnessOpts{
		ClaimProducers: config.RemoteClaimProducersConfig{
			ClaimProducers: map[string]config.ClaimProducerEntry{
				"queue-store": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})
	h.Stub.WhenType("worker").
		Error("boom_requeue", map[string]any{"why": "requeue-branch"}).
		Then().Success(map[string]any{"ok": true}, true, "ran-after-requeue")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "tmpl-error-policy-release-and-requeue", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "worker",
					Executor: "stub",
					ErrorTypes: map[string]node.ErrorTypePolicy{
						"stub/boom_requeue": {Action: "release_and_requeue"},
					},
				},
				scenario.WithClaimProducers(scenario.WriteClaimRef("queue-store", "@queue")),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-tmpl-err-requeue", map[string]any{})

	worker := h.FindNode(iid, "worker")
	require.NotNil(t, worker)

	h.WaitForNodeState(worker.ID, cascade.NodeStateFresh)

	calls := sub.Calls()
	require.Equal(t, 2, countCalls(calls, "open"),
		"release_and_requeue must force a fresh Open for the re-dispatch, not silently reuse "+
			"the errored dispatch's claim — one Open for the errored attempt, one more for "+
			"the requeued re-acquire")
	require.Equal(t, 1, countCalls(calls, "abandon"),
		"the held claim from the errored dispatch must be Abandoned (release_and_requeue "+
			"releases held claims), not left dangling or silently Committed")
	require.Equal(t, 1, countCalls(calls, "commit"),
		"the second, successful dispatch's freshly-reacquired claim must Commit normally")

	var openClaimID, abandonClaimID, commitClaimID string
	for _, c := range calls {
		switch c.Verb {
		case "open":
			if openClaimID == "" {
				openClaimID = c.ClaimID
			}
		case "abandon":
			abandonClaimID = c.ClaimID
		case "commit":
			commitClaimID = c.ClaimID
		}
	}
	require.Equal(t, openClaimID, abandonClaimID,
		"the Abandon must target the claim minted by the errored dispatch's Open")
	require.NotEqual(t, abandonClaimID, commitClaimID,
		"the Commit must target a freshly-minted claim from the requeued re-acquire, not the abandoned one")

	dispatchCount := 0
	for _, o := range h.Stub.Observed() {
		if o.NodeType == "worker" {
			dispatchCount++
		}
	}
	require.Equal(t, 2, dispatchCount,
		"release_and_requeue must produce exactly two worker dispatches: the one that errored "+
			"and the fresh re-acquire that succeeded")

	var eventCount int
	h.QueryRowSQL(
		`SELECT count(*) FROM rimsky_events WHERE node_id = $1 AND kind = 'transient/release_and_requeue/stub/boom_requeue'`,
		[]any{worker.ID}, &eventCount)
	require.Equal(t, 1, eventCount,
		"release_and_requeue must emit exactly one transient/release_and_requeue/<class> audit row")

	var lhCount int
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT count(*) FROM rimsky_claim_handles lh
		   JOIN rimsky_nodes n ON n.id = lh.holder_node_id
		  WHERE n.instance_id = $1 AND lh.is_held = TRUE`, iid,
	).Scan(&lhCount))
	require.Zero(t, lhCount,
		"no claim-handle row may remain held after the node settles fresh")
}
