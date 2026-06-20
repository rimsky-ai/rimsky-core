// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/control/config"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
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
	t.Run("discard_claims_then_retry_releases_claims_before_re_dispatch",
		testTemplateErrorPolicyDiscardClaimsThenRetry)
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
						Policy: []node.PolicyAction{{Action: "pass"}},
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

	var workerRow *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().Get(h.Ctx, worker.ID, tx)
		workerRow = r
		return err
	}))
	require.Equal(t, cascade.NodeStateFresh, workerRow.State,
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
						Policy: []node.PolicyAction{{Action: "give_up"}},
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

	var downstreamRow *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().Get(h.Ctx, downstream.ID, tx)
		downstreamRow = r
		return err
	}))
	require.Equal(t, cascade.NodeStateFresh, downstreamRow.State,
		"give_up must skip downstream — the downstream subscribes on terminal/success and the worker's give_up emits terminal/error/<class>, which must not cascade to it")

	dsID := downstream.ID
	require.Empty(t,
		eventwait.Events(h.Ctx, t, h.Persist, eventwait.Matcher{NodeID: &dsID, Kind: "work_started", KindPrefix: "terminal/"}),
		"downstream must leave no dispatch/terminal events on the ledger when give_up fires upstream")

	for _, o := range h.Stub.Observed() {
		require.NotEqual(t, "downstream", o.NodeType,
			"downstream executor must not be invoked when give_up fires on the upstream worker")
	}

	var workerRow *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().Get(h.Ctx, worker.ID, tx)
		workerRow = r
		return err
	}))
	require.NotNil(t, workerRow.SettlingSignalType)
	require.Contains(t, *workerRow.SettlingSignalType, "terminal/error/",
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
				Type:     "worker",
				Executor: "stub",
				ErrorTypes: map[string]node.ErrorTypePolicy{
					"stub/boom_retry": {
						Policy: []node.PolicyAction{
							{Action: "retry", Count: retryCount, BaseDelayMs: 50},
							{Action: "give_up"},
						},
					},
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

	var raw []byte
	h.QueryRowSQL(
		`SELECT payload::text FROM rimsky_events WHERE node_id = $1 AND kind LIKE 'transient/retry/%' ORDER BY occurred_at ASC LIMIT 1`,
		[]any{worker.ID},
		&raw,
	)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(raw, &payload))
	dc, ok := payload["discarded_claims"].(bool)
	require.True(t, ok,
		"retry payload must carry discarded_claims field (concept:signal contract); got %T", payload["discarded_claims"])
	require.False(t, dc,
		"plain `retry` must record discarded_claims=false in the transient/retry payload — this is the discriminator vs. discard_claims_then_retry")
}

func testTemplateErrorPolicyDiscardClaimsThenRetry(t *testing.T) {
	t.Parallel()

	endpoint, _, teardown := stubfixture.Start(t, stubstore.Config{
		Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
		PickPolicies: map[string]stubstore.PickPolicyConfig{
			"@queue": {
				OnCommit: action.Action{Kind: action.Pop},
				OnGiveUp: action.Action{Kind: action.Recycle},
				InitialItems: []json.RawMessage{
					json.RawMessage(`{"i":1}`),
					json.RawMessage(`{"i":2}`),
					json.RawMessage(`{"i":3}`),
					json.RawMessage(`{"i":4}`),
				},
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

	h.Stub.WhenType("acquirer").Error("boom_discard", map[string]any{"why": "discard-branch"})
	h.Stub.WhenType("inheritor").Success(map[string]any{}, true, "must-not-run")

	const retryCount = 2
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "tmpl-error-policy-discard", Version: "1",
		Nodes: []node.TemplateNodeDef{
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "acquirer",
					Executor: "stub",
					ErrorTypes: map[string]node.ErrorTypePolicy{
						"stub/boom_discard": {
							Policy: []node.PolicyAction{
								{Action: "discard_claims_then_retry", Count: retryCount, BaseDelayMs: 50},
								{Action: "give_up"},
							},
						},
					},
				},
				scenario.WithClaimProducers(scenario.AliasedClaimRef("queue-store", "@queue", "rw", "held")),
			),
			scenario.MakeNode(
				node.TemplateNodeDef{
					Type:     "inheritor",
					Executor: "stub",
					Holds: map[string]node.HoldsBinding{
						"held": {From: "acquirer"},
					},
				},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "acquirer", Type: "terminal/success",
					WakeOnChange:         node.BoolPtr(true),
					ForceUpstreamRefresh: node.BoolPtr(false),
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-tmpl-err-discard", map[string]any{})

	acq := h.FindNode(iid, "acquirer")
	require.NotNil(t, acq)

	require.True(t, h.WaitForNodeState(acq.ID, cascade.NodeStateFailed, 60*time.Second),
		"discard_claims_then_retry chain must exhaust and fall through to give_up; reaching failed proves both the re-dispatches happened and the chain advanced")

	dispatchCount := 0
	for _, o := range h.Stub.Observed() {
		if o.NodeType == "acquirer" {
			dispatchCount++
		}
	}
	require.GreaterOrEqual(t, dispatchCount, retryCount+1,
		"discard_claims_then_retry must re-dispatch like retry; expected at least %d dispatches (initial + %d retries); got %d",
		retryCount+1, retryCount, dispatchCount)

	var raw []byte
	h.QueryRowSQL(
		`SELECT payload::text FROM rimsky_events WHERE node_id = $1 AND kind LIKE 'transient/retry/%' ORDER BY occurred_at ASC LIMIT 1`,
		[]any{acq.ID},
		&raw,
	)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(raw, &payload))
	dc, ok := payload["discarded_claims"].(bool)
	require.True(t, ok,
		"discard_claims_then_retry payload must carry discarded_claims field (concept:signal contract); got %T", payload["discarded_claims"])
	require.True(t, dc,
		"`discard_claims_then_retry` must record discarded_claims=true in the transient/retry payload — this is the discriminator vs. plain retry, the action's wire-level effect")

	claimHandleIDs := listClaimHandleIDsForInstance(t, h, iid)
	require.NotEmpty(t, claimHandleIDs,
		"the acquirer's dispatches must have produced at least one rimsky_claim_handles row against the queue store; "+
			"none means the claim never acquired and the discard semantics weren't exercised")
	releasedRows := 0
	for _, chID := range claimHandleIDs {
		holdersURL := h.ControlBase + "/v1/lock-holders/" + chID.String() + "/claim-holders"
		resp, err := http.Get(holdersURL)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode,
			"claim-holders endpoint must return 200 for a real claim_handle_id")
		var body struct {
			Holders []struct {
				ID            string `json:"id"`
				ClaimHandleID string `json:"claim_handle_id"`
				HolderRunID   string `json:"holder_run_id"`
				State         string `json:"state"`
			} `json:"holders"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
		resp.Body.Close()
		for _, h := range body.Holders {
			require.Equal(t, chID.String(), h.ClaimHandleID,
				"every returned holder must key on the queried claim_handle_id")
			require.NotEqual(t, string(persistence.ClaimHolderStateActive), h.State,
				"GET /v1/lock-holders/%s/claim-holders returned an 'active' holder row after the discard_claims_then_retry chain completed; "+
					"discard must release the holder before re-dispatch (Falsifier: action effect doesn't match declaration)",
				chID.String())
			releasedRows++
		}
	}
	require.Greater(t, releasedRows, 0,
		"at least one claim-holder row must have been read through the /v1/lock-holders/{id}/claim-holders surface to prove the release reached the operator-facing API")
}

func listClaimHandleIDsForInstance(t *testing.T, h *scenario.Harness, instanceID shared.UUID) []shared.UUID {
	t.Helper()
	var ids []shared.UUID
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ids = ids[:0]
		h.QuerySQL(
			`SELECT lh.id FROM rimsky_claim_handles lh
			   JOIN rimsky_nodes n ON n.id = lh.holder_node_id
			  WHERE n.instance_id = $1
			  ORDER BY lh.claimed_at ASC`,
			[]any{uuid.UUID(instanceID)},
			func(scan func(...any) error) error {
				var id shared.UUID
				if err := scan(&id); err != nil {
					return err
				}
				ids = append(ids, id)
				return nil
			},
		)
		if len(ids) > 0 {
			return ids
		}
		time.Sleep(50 * time.Millisecond)
	}
	return ids
}
