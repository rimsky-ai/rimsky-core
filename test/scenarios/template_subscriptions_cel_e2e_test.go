// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// STORY-template-subscriptions proof — exercises the template-author
// subscription surface (canonical signal type-path + optional CEL
// predicate + trailing-`*` prefix) against the real assembled product:
// the scenario harness boots an in-process control-api + supervisor +
// stub executor and drives a real message-emit through
// `POST /v1/instances/{id}/messages`. The cascade walker in
// lib/runtime/message_delivery.go::cascadeMessageSubscribersInTx
// computes the canonical message signal type-path, evaluates each
// subscriber's CEL `when:` predicate against the live signal payload,
// and stale-marks the subscribed node only on a match. The supervisor
// then re-dispatches each stale-marked node — observable through the
// stub's per-node-type dispatch count.
//
// The story's Acceptance asks three things:
//
//  1. A subscription with a CEL predicate fires the node when the
//     emitted signal's payload satisfies the predicate.
//  2. The same subscription does NOT fire on a non-matching payload.
//  3. A trailing-`*` prefix subscription matches every type-path with
//     that prefix.
//
// All three are exhibited end-to-end below: messages are POSTed
// through the real control-api route, the supervisor's frame engine
// promotes a queued frame and the delivery sweep drives the cascade,
// and the stub records the re-dispatches the cascade triggered.
//
// IMPORTANT: a message envelope's `target` field both (a) names the
// `<target>` segment of the emitted `message/<kind>/<sender_kind>/<target>`
// signal type-path AND (b) determines the *frame source* — the node
// whose row is stale-marked by the frame engine when the queued frame
// promotes. The frame source dispatches unconditionally on every
// message (it's how the frame engine wakes a stalled instance), so a
// subscriber that's ALSO the message target would dispatch regardless
// of its subscription CEL — masking the CEL filter under test. To
// keep the CEL filter as the only dispatch gate for the subscribers,
// this test uses a dedicated `frame_anchor` node as the message
// target — `frame_anchor` itself is the frame source and dispatches
// on every message, while the three subscriber receivers
// (`receiver_strict` / `receiver_prefix` / `receiver_other`) fire
// only via the cross-cutting subscription cascade, which is the
// CEL-gated path the story tests.

package scenarios

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// TestTemplateSubscriptions_CELPredicateAndPrefix drives the three
// Acceptance legs of STORY-template-subscriptions through the real
// assembled product. See file header.
//
// Template shape:
//
//   - frame_anchor: a "frame-source" node that is the target of every
//     test message. The frame engine stale-marks the frame source
//     when promoting a queued frame to running, so frame_anchor
//     dispatches on every POSTed message. frame_anchor declares NO
//     subscriptions — it's the deliberate frame-source role, not a
//     subscriber, so the three test legs below can attribute each
//     subscriber dispatch to the CEL-gated cascade rather than to the
//     frame-source wake.
//
//   - receiver_strict: declares a cross-cutting (`instance: true`)
//     subscription to the exact signal type-path
//     `message/invalidate/operator/frame_anchor` with the CEL
//     predicate `payload.message_payload.tenant == "alpha"`. Fires
//     only when both the type-path matches AND the CEL evaluates
//     true. (The signal type-path's `<target>` segment is `frame_anchor`
//     because the cascade walker emits one signal per delivered
//     message with the message's target literal in the last segment.)
//
//   - receiver_prefix: declares a cross-cutting subscription to the
//     trailing-`*` prefix type-path `message/invalidate/*` with NO
//     `when:`. Matches every `message/invalidate/...` type-path the
//     cascade walker emits, regardless of payload — this is the
//     "trailing-`*` prefix matches every type-path with that prefix"
//     acceptance leg.
//
//   - receiver_other: declares a cross-cutting subscription to a
//     DIFFERENT exact type-path
//     `message/invalidate/operator/some_other_target` with no
//     `when:`. Pins the prefix's broadness against an exact path that
//     should fire ONLY when its specific target is named — without
//     this control, an over-broad prefix match couldn't be
//     distinguished from a benign target-specific match.
//
// Flow:
//
//  1. Initial frame: all four nodes (frame_anchor + 3 receivers) are
//     roots and dispatch once on the instance's first frame.
//
//  2. LEG 1 — message with target=frame_anchor, payload.tenant=alpha.
//     Cascade walker emits signal
//     `message/invalidate/operator/frame_anchor`:
//
//     - receiver_strict: type-path matches exact subscription; CEL
//     evaluates `payload.message_payload.tenant == "alpha"` → true;
//     FIRES.
//     - receiver_prefix: type-path is matched by the
//     `message/invalidate/*` prefix; no CEL; FIRES.
//     - receiver_other: subscription is to a DIFFERENT exact
//     type-path; does NOT fire.
//     - frame_anchor: frame-source dispatch; fires unconditionally.
//
//  3. LEG 2 — message with target=frame_anchor, payload.tenant=beta.
//     Same signal type-path as leg 1; CEL evaluates false:
//
//     - receiver_strict: type matches but CEL=false → DOES NOT fire.
//     - receiver_prefix: still matches the type-prefix unconditionally
//     → FIRES.
//     - receiver_other: does not fire.
//     - frame_anchor: frame-source dispatch; fires.
//
//  4. LEG 3 — message with target=some_other_target. Cascade walker
//     emits signal `message/invalidate/operator/some_other_target`:
//
//     - receiver_strict: exact path
//     `…/frame_anchor` ≠ `…/some_other_target` → does NOT fire.
//     - receiver_prefix: `message/invalidate/*` prefix STILL matches
//     this DIFFERENT type-path → FIRES. This is the load-bearing
//     prefix acceptance leg: every type-path under the prefix must
//     fire the subscription, regardless of which specific target it
//     names. (Note: a message with target=some_other_target would
//     normally be refused by `resolveMessageFrameSource` if no node
//     of that type exists; we declare a `some_other_target` node
//     below so the target resolves and the cascade walker emits the
//     distinct type-path.)
//     - receiver_other: exact path matches → FIRES.
//     - some_other_target: frame-source dispatch; fires.
//
// Expected per-subscriber dispatch totals after all four steps:
//
//   - receiver_strict: 1 (initial) + 1 (leg-1 match) = 2.
//   - receiver_prefix: 1 (initial) + 3 (every leg matches the prefix)
//     = 4.
//   - receiver_other: 1 (initial) + 1 (leg-3 exact match) = 2.
//
// The frame-source nodes (`frame_anchor`, `some_other_target`)
// dispatch on each of their respective targeted messages, but those
// dispatches are NOT what's under test — they're the message
// envelope's frame-source machinery, exercised here only to drive
// the cascade walker against the subscribers.
//
// Falsifier coverage:
//
//   - "Subscription fires the node on a non-matching payload (predicate
//     ignored)": leg 2 asserts receiver_strict's count stays at 2
//     after the tenant=beta message. If it advances, the CEL was
//     bypassed.
//   - "Doesn't fire on a matching one": leg 1 asserts
//     receiver_strict's count reaches 2 (initial + alpha-match).
//   - "Trailing-`*` doesn't match its prefix": leg 3 asserts
//     receiver_prefix's count reaches 4 (it fires on every leg,
//     including the DIFFERENT-target leg, proving the prefix matches
//     every type-path under it).
func TestTemplateSubscriptions_CELPredicateAndPrefix(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{})

	// Script each node's dispatch as Success with changed=true so the
	// supervisor's terminal-handler settles the node back to fresh
	// each time. changed=true mirrors the production shape of a
	// reactive node that updates state on each fire; for the test's
	// purposes only the dispatch count matters.
	h.Stub.WhenType("frame_anchor").Success(map[string]any{"observed": 1}, true, "anchor")
	h.Stub.WhenType("some_other_target").Success(map[string]any{"observed": 1}, true, "other-anchor")
	h.Stub.WhenType("receiver_strict").Success(map[string]any{"observed": 1}, true, "strict")
	h.Stub.WhenType("receiver_prefix").Success(map[string]any{"observed": 1}, true, "prefix")
	h.Stub.WhenType("receiver_other").Success(map[string]any{"observed": 1}, true, "other")

	tid := h.DeployTemplate(node.TemplateSpec{
		Name: "template-subscriptions-cel-e2e", Version: "1",
		// Serial-queue delivery: one message per frame. Each cascade
		// walk is independent so per-receiver fire counts can be
		// asserted deterministically. Under coalesce, two messages
		// landing in one frame would deliver together and the
		// once-per-frame cascade guard could conflate dispatches across
		// what the operator-template author would see as separate
		// reactive events.
		FrameResolutionMode: node.FrameResolutionSerialQueue,
		Nodes: []node.TemplateNodeDef{
			// frame_anchor + some_other_target are the deliberate
			// frame-source roles. They have NO subscriptions; the
			// cascade walker never matches against them. They dispatch
			// only on the frame-source wake when a message targets
			// their node-type.
			scenario.MakeNode(node.TemplateNodeDef{Type: "frame_anchor", Executor: "stub"}),
			scenario.MakeNode(node.TemplateNodeDef{Type: "some_other_target", Executor: "stub"}),
			// receiver_strict: exact-type + CEL predicate. The
			// type-path's `<target>` segment is `frame_anchor` (not
			// `receiver_strict`) because the cascade walker emits the
			// signal with the message's literal target segment, which
			// is `frame_anchor` for legs 1 + 2.
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "receiver_strict", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Instance: true,
					Type:     "message/invalidate/operator/frame_anchor",
					When:     `payload.message_payload.tenant == "alpha"`,
					Frame:    "in",
				}),
			),
			// receiver_prefix: trailing-`*` prefix. Matches every
			// type-path with the `message/invalidate/` prefix.
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "receiver_prefix", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Instance: true,
					Type:     "message/invalidate/*",
					Frame:    "in",
				}),
			),
			// receiver_other: exact-type targeting a DIFFERENT segment.
			// Fires only when leg 3's message names `some_other_target`.
			scenario.MakeNode(
				node.TemplateNodeDef{Type: "receiver_other", Executor: "stub"},
				scenario.WithSubscribes(node.SubscriptionEntry{
					Instance: true,
					Type:     "message/invalidate/operator/some_other_target",
					Frame:    "in",
				}),
			),
		},
	})
	iid := h.CreateInstance(tid, "ck-template-subs-cel", map[string]any{})

	// All five roots reach fresh after the initial dispatch.
	for _, nt := range []string{
		"frame_anchor", "some_other_target",
		"receiver_strict", "receiver_prefix", "receiver_other",
	} {
		n := h.FindNode(iid, nt)
		require.NotNil(t, n, "node %s missing from instance", nt)
		require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateFresh, 30*time.Second),
			"%s should reach fresh from its initial frame", nt)
	}

	// Baseline: each node fired exactly once from the initial frame.
	// Asserted with overshoot-check so a buggy cascade can't masquerade
	// as a benign re-dispatch.
	requireEventualCount(t, h, "receiver_strict", 1, 10*time.Second,
		"exactly 1 initial dispatch of receiver_strict expected before any messages")
	requireEventualCount(t, h, "receiver_prefix", 1, 10*time.Second,
		"exactly 1 initial dispatch of receiver_prefix expected before any messages")
	requireEventualCount(t, h, "receiver_other", 1, 10*time.Second,
		"exactly 1 initial dispatch of receiver_other expected before any messages")

	// ---------------------------------------------------------------------
	// LEG 1 — target=frame_anchor, tenant=alpha. Cascade walker emits
	// signal `message/invalidate/operator/frame_anchor`. CEL on
	// receiver_strict evaluates true → fires. receiver_prefix matches
	// type-prefix → fires. receiver_other's exact path is
	// `…/some_other_target` ≠ `…/frame_anchor` → does NOT fire.
	// ---------------------------------------------------------------------
	postMessage(t, h, iid, "frame_anchor", "leg1-anchor-alpha", map[string]any{
		"tenant": "alpha",
	})

	requireEventualCount(t, h, "receiver_strict", 2, 30*time.Second,
		"receiver_strict must fire on the leg-1 message (tenant=alpha) — "+
			"if observed=1 here, the CEL predicate was ignored or the cascade "+
			"walker did not deliver the message to the subscription edge map")
	requireEventualCount(t, h, "receiver_prefix", 2, 30*time.Second,
		"receiver_prefix must fire on the leg-1 message — trailing-`*` prefix "+
			"`message/invalidate/*` must match the emitted type-path "+
			"`message/invalidate/operator/frame_anchor`")
	requireSteadyCount(t, h, "receiver_other", 1, 3*time.Second,
		"receiver_other must not fire on a leg-1 message — its subscription is "+
			"an exact path to a different target segment")

	// ---------------------------------------------------------------------
	// LEG 2 — target=frame_anchor, tenant=beta. Same signal type-path
	// as leg 1, but the CEL predicate evaluates false → receiver_strict
	// must NOT fire. receiver_prefix still matches by type-prefix (no
	// `when:`) → fires. receiver_other still doesn't match.
	// ---------------------------------------------------------------------
	postMessage(t, h, iid, "frame_anchor", "leg2-anchor-beta", map[string]any{
		"tenant": "beta",
	})

	requireEventualCount(t, h, "receiver_prefix", 3, 30*time.Second,
		"receiver_prefix must fire on the leg-2 message — the CEL-less prefix "+
			"subscription matches the type-path regardless of payload")
	// receiver_strict must NOT advance — the CEL predicate gated the
	// cascade. Allow a settling window so any spurious cascade has time
	// to land.
	requireSteadyCount(t, h, "receiver_strict", 2, 3*time.Second,
		"receiver_strict must not fire on a non-matching CEL payload (tenant=beta) — "+
			"if it advanced past 2, the CEL predicate is being ignored and the "+
			"falsifier triggers")
	requireSteadyCount(t, h, "receiver_other", 1, 3*time.Second,
		"receiver_other must not fire on a leg-2 message (still target=frame_anchor)")

	// ---------------------------------------------------------------------
	// LEG 3 — target=some_other_target. Cascade walker emits signal
	// `message/invalidate/operator/some_other_target`. Trailing-`*`
	// prefix MUST match this DIFFERENT type-path — the
	// prefix-matches-every-type-path-with-that-prefix acceptance leg.
	// receiver_other matches by exact path. receiver_strict's exact path
	// is `…/frame_anchor` ≠ `…/some_other_target` → does NOT fire.
	// ---------------------------------------------------------------------
	postMessage(t, h, iid, "some_other_target", "leg3-other", nil)

	requireEventualCount(t, h, "receiver_other", 2, 30*time.Second,
		"receiver_other must fire when a message names its exact target segment")
	requireEventualCount(t, h, "receiver_prefix", 4, 30*time.Second,
		"receiver_prefix must fire on the leg-3 message — trailing-`*` prefix "+
			"`message/invalidate/*` must match every emitted type-path with that "+
			"prefix; a failure here means the prefix-match falsifier triggered")
	requireSteadyCount(t, h, "receiver_strict", 2, 3*time.Second,
		"receiver_strict must not fire on a message targeting some_other_target "+
			"(leg 3) — its subscription is an exact path to frame_anchor")
}

// requireEventualCount polls the stub's observed-by-node-type count
// until it reaches `want` or the deadline elapses. After reaching
// `want`, asserts exact equality so an overshoot (spurious extra
// dispatch) surfaces as a failure rather than passing silently.
//
// Used for "should fire" assertions where the cascade is racing
// against the test.
func requireEventualCount(
	t *testing.T,
	h *scenario.Harness,
	nodeType string,
	want int,
	timeout time.Duration,
	failMsg string,
) {
	t.Helper()
	require.Eventually(t, func() bool {
		return countByType(h, nodeType) >= want
	}, timeout, 50*time.Millisecond, failMsg+": got %d, want %d", countByType(h, nodeType), want)
	require.Equal(t, want, countByType(h, nodeType),
		"%s: dispatch count overshot expected value", failMsg)
}

// requireSteadyCount asserts that the stub's observed-by-node-type
// count stays at `want` across the entire settling window. Used for
// "should NOT fire" assertions where a spurious cascade would
// increment the count.
func requireSteadyCount(
	t *testing.T,
	h *scenario.Harness,
	nodeType string,
	want int,
	window time.Duration,
	failMsg string,
) {
	t.Helper()
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		got := countByType(h, nodeType)
		if got != want {
			t.Logf("Observed dump on steady-state failure:")
			for i, o := range h.Stub.Observed() {
				t.Logf("  [%d] NodeType=%s attrs=%v", i, o.NodeType, o.Attributes)
			}
			t.Fatalf("%s: count=%d, want=%d (steady-state violated within %s)",
				failMsg, got, want, window)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// countByType returns the number of stub-observed dispatches for the
// given node-type. The stub's Observed() returns a snapshot of every
// gRPC Execute call the supervisor made; counting by NodeType yields
// the per-node-type dispatch total.
func countByType(h *scenario.Harness, nodeType string) int {
	var n int
	for _, o := range h.Stub.Observed() {
		if o.NodeType == nodeType {
			n++
		}
	}
	return n
}

// postMessage POSTs an operator-sender `invalidate` message to the
// instance's message endpoint with the canonical Idempotency-Key
// header (mandatory per @blessed-invariant on universal idempotency).
// The body's `target` selects which downstream node-type the
// emitted-signal's type-path resolves to (the cascade walker uses
// `message/<kind>/<sender_kind>/<target>` per concept:signal) AND
// determines the frame source whose row the frame engine
// stale-marks on frame promotion. `payload` is JSON-marshaled into
// the envelope; the cascade walker surfaces it under
// `payload.message_payload` in the signal envelope passed to CEL
// `when:` predicates per
// `lib/foundation/signal/payloads.go::MessagePayload`.
func postMessage(
	t *testing.T,
	h *scenario.Harness,
	instanceID shared.UUID,
	target, idempotencyKey string,
	messagePayload map[string]any,
) {
	t.Helper()
	body := map[string]any{
		"kind":   "invalidate",
		"target": target,
	}
	if messagePayload != nil {
		raw, err := json.Marshal(messagePayload)
		require.NoError(t, err, "marshal message payload")
		body["payload"] = json.RawMessage(raw)
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err, "marshal message body")
	url := h.ControlBase + "/v1/instances/" + instanceID.String() + "/messages"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	require.NoError(t, err, "build POST message")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", idempotencyKey)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err, "POST message")
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode,
		"POST %s/messages target=%s key=%s expected 201 Created", url, target, idempotencyKey)
}
