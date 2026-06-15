// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// STORY-template-error-policy acceptance proof (Pass 37 of
// .ok-planner/plans/2026-06-08-design-corpus-bootstrap.md).
//
// The story: a template author declares per-error-class routing actions
// (`pass`, `give_up`, `retry`, `discard_claims_then_retry`) and the
// runtime honors each action at the appropriate error site. The
// Falsifier the validator will argue: "Any of the four actions has no
// observable effect (the runtime acts the same regardless of the
// declared action), OR an action's effect doesn't match the
// declaration."
//
// Each of the four sub-tests below drives a node through a real
// dispatch where the executor raises an error mapped to one specific
// action, then asserts the OBSERVABLE effect the spec names:
//
//   - pass: cascade continues (a downstream subscriber on terminal/*
//     fires) AND the node settles fresh (not failed).
//   - give_up: node-run terminates (state=failed) AND downstream nodes
//     are not dispatched (they remain fresh).
//   - retry: the runtime re-dispatches (multiple ExecuteRequest
//     observations) AND records transient/retry/<n>/<class> signals.
//   - discard_claims_then_retry: the runtime re-dispatches AND records
//     the `discarded_claims: true` flag on the retry signal payload AND
//     releases held claims (observable via `GET /v1/lock-holders/{id}/
//     claim-holders` — the holder row leaves state=active for the
//     released claim).
//
// All four sub-tests run on the assembled product (control-api over HTTP,
// real scheduler + supervisor + stub executor + stub claim-producer),
// so each action's effect is exhibited through the real value-delivery
// path — no stubs of the runtime itself.
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
	"github.com/rimsky-ai/rimsky-core/test/support/eventwait"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
	stubstore "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/store"
	stubfixture "github.com/rimsky-ai/rimsky-core/test/support/stores/stub/testfixture"
)

// TestTemplateErrorPolicy exercises the four template-error-policy
// actions end-to-end. Run as a single Go test with four sub-tests; the
// plan's `go test -run TestTemplateErrorPolicy` invocation picks up all
// four.
func TestTemplateErrorPolicy(t *testing.T) {
	t.Parallel()

	t.Run("pass_settles_fresh_and_cascades", testTemplateErrorPolicyPass)
	t.Run("give_up_terminates_node_skips_downstream", testTemplateErrorPolicyGiveUp)
	t.Run("retry_re_dispatches", testTemplateErrorPolicyRetry)
	t.Run("discard_claims_then_retry_releases_claims_before_re_dispatch",
		testTemplateErrorPolicyDiscardClaimsThenRetry)
}

// testTemplateErrorPolicyPass: node A errors with class X mapped to
// `pass`. The Falsifier-relevant observables:
//   - A settles fresh with settling_signal_type=terminal/error/<class>.
//   - A downstream subscriber B on `terminal/*` fires (cascade
//     continues), confirming `pass` propagates as if the node had
//     succeeded.
//
// If the runtime treated pass as give_up, A would land in failed and B
// would still fire (terminal/* matches both); the discriminator is the
// node state. If the runtime treated pass as retry, the executor would
// be invoked repeatedly and A wouldn't settle.
func testTemplateErrorPolicyPass(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("worker").Error("boom_pass", map[string]any{"why": "pass-branch"})

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "tmpl-error-policy-pass", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
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
			// @constraint: Pure-cascade downstream subscribing on terminal/* — fires
			// on any terminal signal. `pass` emits terminal/error/<class>;
			// the cascade-after-pass must propagate that signal so the
			// downstream node settles fresh.
			scenario.MakeNode(node.TemplateNodeDef{Type: "downstream"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Node: "worker", Type: "terminal/*",
				})),
		},
	})
	iid := h.CreateInstance(tid, "ck-tmpl-err-pass", map[string]any{})

	worker := h.FindNode(iid, "worker")
	downstream := h.FindNode(iid, "downstream")
	require.NotNil(t, worker)
	require.NotNil(t, downstream)

	// @deliberate: Observable 1 — worker settles fresh under pass (not failed). The
	// settling_signal_type carries the canonical terminal/error/<class>
	// envelope so subscribers wildcard-matching `terminal/*` see it,
	// even though the run-row color is fresh (the pass "absolve"
	// semantics live on Resolution.Color, not the signal type).
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

	// @deliberate: Observable 2 — the downstream subscriber cascades on the
	// terminal/error/* signal that pass emits. Reaching fresh is the
	// real cascade fire (state transition driven by the cascade walker
	// against the wait-set row inserted at emit time).
	require.True(t, h.WaitForNodeState(downstream.ID, cascade.NodeStateFresh, 30*time.Second),
		"pass must continue the cascade — a downstream subscriber on terminal/* must fire on the worker's terminal/error/<class> signal")

	// @deliberate: Falsifier guard: the executor should not have been re-invoked
	// (pass is a one-shot resolution, not retry).
	dispatchCount := 0
	for _, o := range h.Stub.Observed() {
		if o.NodeType == "worker" {
			dispatchCount++
		}
	}
	require.Equal(t, 1, dispatchCount,
		"pass must NOT re-dispatch — exactly one worker dispatch expected; multiple would mean the runtime treated pass as retry")
}

// testTemplateErrorPolicyGiveUp: node A errors with class X mapped to
// `give_up`. Observables:
//   - A settles failed (state=failed).
//   - A downstream subscriber on terminal/success does NOT fire (the
//     give_up terminal/error/<class> signal doesn't match
//     terminal/success), confirming the downstream is genuinely
//     skipped on give_up.
//
// We use `terminal/success` (not `terminal/*`) for the downstream so
// the discriminator is purely the give_up vs. pass behavior: a give_up
// emits terminal/error/<class> which doesn't match terminal/success,
// while a hypothetical mis-routed pass-to-success would.
func testTemplateErrorPolicyGiveUp(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("worker").Error("boom_giveup", map[string]any{"why": "give-up-branch"})
	h.Stub.WhenType("downstream").Success(map[string]any{}, true, "must-not-run")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "tmpl-error-policy-giveup", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
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
				})),
		},
	})
	iid := h.CreateInstance(tid, "ck-tmpl-err-giveup", map[string]any{})

	worker := h.FindNode(iid, "worker")
	downstream := h.FindNode(iid, "downstream")
	require.NotNil(t, worker)
	require.NotNil(t, downstream)

	// @constraint: Observable 1 — worker reaches failed.
	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateFailed, 30*time.Second),
		"give_up must drive the worker to state=failed; pass would settle fresh and retry would not settle at all")

	// @constraint: Observable 2 — downstream is genuinely skipped. The give_up
	// terminal signal is terminal/error/<class>; the downstream
	// subscribes on terminal/success which is structurally disjoint
	// from terminal/error/*. The downstream must NOT have been
	// dispatched. Allow a grace window so any stray cascade tick would
	// be observed.
	time.Sleep(2 * time.Second)

	var downstreamRow *persistence.NodeRow
	require.NoError(t, h.InTx(func(tx persistence.Tx) error {
		r, err := h.Persist.Nodes().Get(h.Ctx, downstream.ID, tx)
		downstreamRow = r
		return err
	}))
	require.Equal(t, cascade.NodeStateFresh, downstreamRow.State,
		"give_up must skip downstream — the downstream subscribes on terminal/success and the worker's give_up emits terminal/error/<class>, which must not cascade to it")

	// @constraint: Durable-record check (2026-06-11 polling audit): the fresh-state
	// sample above cannot distinguish "downstream never ran" from
	// "downstream spuriously ran and settled back to fresh inside the
	// grace window". The append-only event log can — any dispatch
	// leaves work_started / terminal/* rows that no later transition
	// erases.
	dsID := downstream.ID
	require.Empty(t,
		eventwait.Events(h.Ctx, t, h.Persist, eventwait.Matcher{NodeID: &dsID, Kind: "work_started", KindPrefix: "terminal/"}),
		"downstream must leave no dispatch/terminal events on the ledger when give_up fires upstream")

	// @constraint: Falsifier guard: the downstream's executor must not have been
	// invoked. h.Stub.Observed() only records worker dispatches.
	for _, o := range h.Stub.Observed() {
		require.NotEqual(t, "downstream", o.NodeType,
			"downstream executor must not be invoked when give_up fires on the upstream worker")
	}

	// @deliberate: Falsifier guard: worker's settling signal must carry the
	// terminal/error/<class> envelope (give_up's wire shape).
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

// testTemplateErrorPolicyRetry: node A errors with class X mapped to
// `retry` with Count > 1. Observables:
//   - The runtime re-dispatches the node multiple times (multiple
//     ExecuteRequest observations) — the action's effect must be a
//     real re-dispatch through the supervisor's actual retry path.
//   - Each retry emits a transient/retry/<attempt>/<class> signal
//     (audit-row visible in rimsky_events).
//   - After the retry chain exhausts, the node lands in a terminal
//     state (we follow with give_up to settle deterministically).
//
// The retry count is small (Count=3 + give_up) so the test runs
// quickly; with a small backoff the retries happen in a tight loop.
func testTemplateErrorPolicyRetry(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	h.Stub.WhenType("worker").Error("boom_retry", map[string]any{"why": "retry-branch"})

	const retryCount = 3
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "tmpl-error-policy-retry", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
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

	// @constraint: Observable — node lands in failed once the retry chain
	// exhausts and falls through to give_up.
	require.True(t, h.WaitForNodeState(worker.ID, cascade.NodeStateFailed, 60*time.Second),
		"retry chain must run to exhaustion then fall through to give_up; reaching failed proves both the retry dispatches happened and the chain advanced past the retry slot")

	// @deliberate: Observable — multiple worker dispatches occurred. The initial
	// dispatch plus retryCount retries = retryCount+1 minimum. Allow
	// a small slop because the runner_acquire path may emit duplicate
	// requests under heavy parallel testcontainer load.
	dispatchCount := 0
	for _, o := range h.Stub.Observed() {
		if o.NodeType == "worker" {
			dispatchCount++
		}
	}
	require.GreaterOrEqual(t, dispatchCount, retryCount+1,
		"retry must produce at least %d worker dispatches (initial + %d retries); got %d — the runtime did not re-dispatch on retry",
		retryCount+1, retryCount, dispatchCount)

	// @deliberate: Observable — transient/retry/<n>/<class> signals appear in the
	// canonical audit log (rimsky_events). Each retry emits one row;
	// the row's kind carries the wire signal type-path.
	var retryEventCount int
	h.QueryRowSQL(
		`SELECT count(*) FROM rimsky_events WHERE node_id = $1 AND kind LIKE 'transient/retry/%'`,
		[]any{worker.ID},
		&retryEventCount,
	)
	require.GreaterOrEqual(t, retryEventCount, retryCount,
		"each retry must emit a transient/retry/<n>/<class> audit row; expected at least %d, got %d",
		retryCount, retryEventCount)

	// @deliberate: Observable — the retry signal payload's `discarded_claims` flag
	// is FALSE (this is the discriminator vs.
	// discard_claims_then_retry).
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

// testTemplateErrorPolicyDiscardClaimsThenRetry: node A holds a claim
// from a stub claim-producer in a held subgraph, then errors with
// class X mapped to `discard_claims_then_retry`. Observables:
//   - The runtime re-dispatches the node (same as plain retry).
//   - The retry signal payload records `discarded_claims: true` (the
//     wire-level discriminator vs. plain retry).
//   - The held claim's lock-holder row is released — observable via
//     `GET /v1/lock-holders/{claim_handle_id}/claim-holders`: the
//     holder row's state leaves 'active' (becomes 'completed' or
//     'failed') across the retry chain. The HTTP route is the exact
//     operator-facing surface the spec names.
//
// Why a HELD claim (alias=held + inheritor's Holds binding):
// non-held claims have no rimsky_claim_holders rows — the release
// path goes through `ResolveClaimHandleTerminal` which mutates only
// the claim-handle row's state. Held claims have explicit holder
// rows (one per holder-run) that the `GET /v1/lock-holders/{id}/
// claim-holders` route surfaces. The spec story names that route
// explicitly as the observable surface, so the test must exercise it
// — held-claim shape is the only configuration where the route's
// response is non-empty.
//
// We use a Count=2 retry chain followed by give_up so the test ends
// deterministically in failed; with 2 retries we see at least one full
// "discard then re-acquire then re-dispatch" cycle in the
// rimsky_claim_handles ledger.
func testTemplateErrorPolicyDiscardClaimsThenRetry(t *testing.T) {
	t.Parallel()

	// @deliberate: Stub queue-store with enough items to satisfy multiple
	// re-acquisitions across the discard_claims_then_retry chain. The
	// fixed @queue selector pops one item per acquisition; we seed
	// several so each retry's re-acquire path finds an item.
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
		Stores: config.RemoteStoresConfig{
			Stores: map[string]config.StoreEntry{
				"queue-store": {
					Endpoint:     "grpc://" + endpoint,
					Capabilities: claimproducer.Capabilities{WriteSemanticsAllowed: []claimproducer.WriteSemantics{claimproducer.WriteSemanticsSync}},
				},
			},
		},
	})

	h.Stub.WhenType("acquirer").Error("boom_discard", map[string]any{"why": "discard-branch"})
	// @constraint: Inheritor's executor never invoked — its subscription is on
	// terminal/success which the acquirer's terminal/error/<class>
	// settlement (after give_up exhaustion) does not match. The
	// inheritor is present so the acquirer's claim is a HELD claim
	// (the held branch in releaseClaim populates rimsky_claim_holders
	// rows that the /v1/lock-holders/{id}/claim-holders surface reads).
	h.Stub.WhenType("inheritor").Success(map[string]any{}, true, "must-not-run")

	const retryCount = 2
	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "tmpl-error-policy-discard", Version: "1",
		FrameResolutionMode: node.FrameResolutionSerialQueue,
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
				scenario.WithStores(scenario.AliasedClaimRef("queue-store", "@queue", "rw", "held")),
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
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-tmpl-err-discard", map[string]any{})

	acq := h.FindNode(iid, "acquirer")
	require.NotNil(t, acq)

	// @deliberate: Observable 1 — node ultimately lands in failed once the
	// discard_claims_then_retry chain exhausts and falls through to
	// give_up. The fall-through proves the chain was actually
	// traversed.
	require.True(t, h.WaitForNodeState(acq.ID, cascade.NodeStateFailed, 60*time.Second),
		"discard_claims_then_retry chain must exhaust and fall through to give_up; reaching failed proves both the re-dispatches happened and the chain advanced")

	// @deliberate: Observable 2 — multiple acquirer dispatches occurred. Each
	// `discard_claims_then_retry` step re-enqueues the dispatch the
	// same way plain `retry` does, just with the claim-release flag
	// set on the resolution.
	dispatchCount := 0
	for _, o := range h.Stub.Observed() {
		if o.NodeType == "acquirer" {
			dispatchCount++
		}
	}
	require.GreaterOrEqual(t, dispatchCount, retryCount+1,
		"discard_claims_then_retry must re-dispatch like retry; expected at least %d dispatches (initial + %d retries); got %d",
		retryCount+1, retryCount, dispatchCount)

	// @deliberate: Observable 3 — the retry signal payload records
	// `discarded_claims: true`. This is the wire-level discriminator
	// vs. plain `retry`, the contract that proves the action's
	// declared intent reached the canonical signal envelope.
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

	// @deliberate: Observable 4 — the held claim-holder row is released through the
	// spec-named operator surface `GET /v1/lock-holders/{id}/claim-holders`.
	// Each acquirer dispatch acquires a fresh claim-handle row against
	// the queue store; on discard_claims_then_retry the supervisor
	// releases the holder row before the next dispatch (via
	// releaseLocksInTx). After the failed terminal, every claim-handle
	// row this instance touched must have a corresponding claim-holders
	// row in a non-active state.
	//
	// Read through the real HTTP route the spec names — proving the
	// operator-facing surface and the supervisor agree on the released
	// state.
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
			// @constraint: Every holder row must have left 'active' — the
			// discard_claims_then_retry release path either completes
			// the holder (on commit semantics) or fails it (on
			// release_failed). Both are non-active. An 'active' row
			// here would mean the claim was NOT released across the
			// retry, falsifying the action declaration.
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

// listClaimHandleIDsForInstance reads rimsky_claim_handles for every
// row whose holder_node_id belongs to the given instance and returns
// each claim_handle_id. Used by the discard_claims_then_retry
// observable to drive each handle through GET /v1/lock-holders/{id}/
// claim-holders.
func listClaimHandleIDsForInstance(t *testing.T, h *scenario.Harness, instanceID shared.UUID) []shared.UUID {
	t.Helper()
	var ids []shared.UUID
	// @constraint: poll briefly — the last release runs in the failed-terminal tx;
	// claim-handles persist across releases (state column flips rather
	// than the row going away) so a snapshot taken right after
	// WaitForNodeState→failed already sees them, but allow a small
	// window for the final commit to settle.
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
