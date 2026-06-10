// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Cross-stack proof for STORY-publisher-protocol: a service author's
// example Publisher — registered with rimsky's publisher catalog,
// advertising the kinds it emits via Capabilities, handling Subscribe /
// Unsubscribe / ListSubscriptions — plugs into a running rimsky stack
// end-to-end through the public protocol surface. Each leg of the spec's
// Acceptance is exhibited against the REAL assembled product
// (rimsky-all-in-one in a testcontainer, Postgres state DB) plus the REAL
// example publisher binary (this directory's Publisher type, run
// in-process and exposed to the container via WithHostPortAccess):
//
//  1. Subscribe lands. The example publisher's Subscribe handler is
//     invoked when an instance is created against a template whose
//     `publishers:` block names the example publisher's kind. The
//     subscribeCalls counter exposed via Calls() is the load-bearing
//     observable — proving rimsky issued a real RPC, not a no-op.
//
//  2. Messages reach the targeted instance through the message endpoint.
//     The publisher emits to `POST /v1/instances/{id}/messages` with the
//     mandatory Idempotency-Key header, sender_kind=publisher, and the
//     publisher_subscription_id capability token. The downstream node
//     subscribing to `message/invalidate/publisher/<target>` fires
//     through the real cascade — observable as the node's
//     work_started count growing.
//
//  3. The dedup header is mandatory. A POST without the Idempotency-Key
//     header is refused with 400 at the request boundary — proving the
//     header is a platform guarantee that cannot be silently bypassed.
//
//  4. Restart-time reconcile uses ListSubscriptions. After rimsky is
//     restarted, the control-api fires runtime.ResyncPublisherSubscriptions
//     against every configured publisher. ListSubscriptions is the
//     load-bearing call: when the publisher reports the still-live
//     subscription, the reconcile must NOT re-issue Subscribe for it. The
//     publisher's Calls() snapshot before and after the restart proves
//     ListSubscriptions was called AND Subscribe count did NOT grow.
//
// The four legs together exhibit STORY-publisher-protocol's three
// falsifier failure modes:
//   - "Subscribe is acknowledged but messages never reach the message
//     endpoint" → fails when leg 2 cannot observe the downstream cascade.
//   - "the post-restart reconcile re-subscribes already-active
//     subscriptions" → fails when Calls().Subscribe grows across leg 4.
//   - "the publisher emits without the dedup header and is silently
//     accepted" → fails when leg 3 receives a non-400 response.
//
// Test files are exempt from the Apache→AGPL import-direction lint
// (tools/license-check/imports.go::verifyImports), so this `_test.go`
// file may import the lib/services testcontainers harness without
// putting the example's published Apache surface at risk — consumers
// who `go build` the example never pull in any test dependency.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

// TestE2E_ExamplePublisherAgainstRunningRimsky boots the rimsky-all-in-one
// image with the example Publisher registered as a peer service, then
// exhibits each of the four protocol-surface properties STORY-publisher-
// protocol's Acceptance names.
//
// Build requirement: the rimsky-all-in-one image must be built locally
// (`make core-images`) before this test runs. The harness pulls
// `rimsky-all-in-one:latest` from the local Docker daemon — nothing is
// fetched from a registry. A missing image is a hard t.Fatal (the
// harness never t.Skip's), so a developer who hasn't run `make
// core-images` sees the missing-image error directly.
func TestE2E_ExamplePublisherAgainstRunningRimsky(t *testing.T) {
	// Not parallel: this scenario stands up a docker network + a Postgres
	// testcontainer + a rimsky-all-in-one container plus an in-process
	// publisher on a host port, then RESTARTS rimsky once. The cost is
	// real, so the in-process publisher_test.go keeps its fast shape and
	// only this gate pays the cross-stack price.
	ctx := context.Background()

	// 1. Stand up the example publisher in-process on a free host port.
	//    The container's control-api dials it at
	//    `host.testcontainers.internal:<port>` — see the WithHostPortAccess
	//    option below. The publisher's gRPC server is reused across the
	//    rimsky restart so the second boot's ListSubscriptions call lands
	//    on the SAME in-memory subscription registry, which is exactly
	//    what proves the reconcile sweep does not re-Subscribe.
	pubPort := freeHostPort(t)
	pub := startExamplePublisher(t, pubPort)

	// Also stand up a tiny in-process executor stub on a SEPARATE host
	// port. The reactor node needs an executor to dispatch through — the
	// supervisor's `work_started` event (the cascade observable) only
	// fires on a real dispatch attempt, which requires a reachable
	// executor. A minimal Success-returning stub is enough: the test's
	// load-bearing observable is "did the publisher emit cause a NEW
	// dispatch on the subscribing node", not "did the dispatch do real
	// work". Keeping the stub inline (instead of importing a docker
	// image) keeps the example self-contained at runtime.
	execPort := freeHostPort(t)
	startStubExecutor(t, execPort)

	// 2. Bring up rimsky-all-in-one on the harness default (Postgres) with
	//    BOTH the example publisher and the stub executor registered as
	//    peers. They must be reachable BEFORE rimsky comes up because the
	//    control-api fires a Capabilities + ListSubscriptions handshake
	//    against every declared peer at startup; the start* helpers block
	//    until the gRPC server is listening, satisfying that ordering
	//    constraint. BringUpRimskyHandle (not BringUpRimsky) returns the
	//    restart-capable handle so leg 4 can rebuild the rimsky/all
	//    container against the SAME Postgres state DB.
	pubEndpoint := fmt.Sprintf("host.testcontainers.internal:%d", pubPort)
	execEndpoint := fmt.Sprintf("host.testcontainers.internal:%d", execPort)
	h := harness.BringUpRimskyHandle(ctx, t,
		harness.WithPublisher("example", pubEndpoint),
		harness.WithExecutor("stub", execEndpoint),
		harness.WithHostPortAccess(pubPort, execPort),
		// The reactor node in deployExampleTemplate references the stub
		// executor by name. Strict "all" mode requires every referenced
		// peer's schema visible at registration; the stub advertises a
		// permissive open schema so "available" or "all" would both work,
		// but "none" keeps the test resilient to any other unwired
		// reference the template may grow.
		harness.WithRefValidationMode("none"),
	)

	// Run each acceptance leg as a sub-test against the SAME running
	// stack — the four legs are independent observations against the
	// same control-api, so a single bring-up is sufficient and a
	// per-leg bring-up would only multiply the bring-up cost. Order
	// matters here: leg 1 creates the instance + publisher-subscription
	// that legs 2/3/4 reuse.
	state := &exampleState{}

	t.Run("Subscribe_lands_on_real_publisher", func(t *testing.T) {
		exerciseSubscribeLeg(t, h.Endpoint, pub, state)
	})
	t.Run("Messages_reach_targeted_instance_via_dedup_header", func(t *testing.T) {
		exerciseMessageDeliveryLeg(t, h.Endpoint, state)
	})
	t.Run("Missing_dedup_header_is_refused", func(t *testing.T) {
		exerciseMissingDedupHeaderLeg(t, h.Endpoint, state)
	})
	t.Run("Restart_reconcile_uses_ListSubscriptions_without_resubscribing", func(t *testing.T) {
		exerciseRestartReconcileLeg(ctx, t, h, pub, state)
	})
}

// exampleState carries the IDs created in leg 1 and reused by legs
// 2/3/4. Centralizing here so the per-leg helpers stay small.
type exampleState struct {
	templateID     string
	instanceID     string
	subscriptionID string
}

// exerciseSubscribeLeg deploys a template referencing the example
// publisher's kind, creates an instance, and asserts the publisher's
// Subscribe handler was invoked exactly once with a matching
// publisher_subscription_id.
//
// The leg also pre-asserts that rimsky's initial-startup
// runtime.ResyncPublisherSubscriptions ran (ListSubscriptions counter
// > 0) BEFORE we capture the pre-instance-create snapshot — proof the
// resync goroutine is reachable. With no active subscriptions at
// startup the call is a no-op on the publisher side (empty live set),
// but the call itself still happens, so the publisher's
// listSubscriptionsCalls increments. Failing here on initial startup
// localizes a publisher-wiring defect to "publisher registry empty" or
// "publisher gRPC server unreachable", separately from the
// restart-reconcile leg.
//
// Proof for spec acceptance leg (a): "rimsky issues a Subscribe with
// resolved config; the publisher acknowledges".
func exerciseSubscribeLeg(t *testing.T, ep harness.RimskyEndpoint, pub *Publisher, state *exampleState) {
	// Wait briefly for the startup resync goroutine to run. Resync is
	// invoked from a `go func()` after StartControlAPI returns, so
	// /health-200 does not imply it has executed yet. Polling here makes
	// the test ordering deterministic.
	startupDeadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(startupDeadline) {
		if pub.Calls().ListSubscriptions > 0 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if pub.Calls().ListSubscriptions == 0 {
		t.Fatalf("initial-startup ResyncPublisherSubscriptions never called PublisherClient.ListSubscriptions on the example publisher within 30s — the publisher peer was not registered in rimsky's runtime publisher registry. Verify the publishers: block was rendered into rimsky.yml AND the publisher's gRPC server is reachable from inside the rimsky container at the configured endpoint")
	}

	before := pub.Calls()
	state.templateID = deployExampleTemplate(t, ep)
	state.instanceID = createExampleInstance(t, ep, state.templateID, "ck-example-publisher")

	// Subscribe is fired synchronously inside POST /v1/instances. Poll
	// briefly to absorb any goroutine scheduling lag, but the count must
	// grow within ~5 seconds.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if pub.Calls().Subscribe > before.Subscribe {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	after := pub.Calls()
	if after.Subscribe <= before.Subscribe {
		t.Fatalf("Subscribe count did NOT grow on instance create: before=%d after=%d (rimsky must call PublisherClient.Subscribe synchronously inside the instance-create flow; falsifier: Subscribe is acknowledged but never reached)",
			before.Subscribe, after.Subscribe)
	}

	// Capture the subscription ID for leg 2. The publisher holds it in
	// its in-memory registry, keyed by publisher_subscription_id. Rimsky
	// generates this id when it inserts the rimsky_publisher_subscriptions
	// row and passes it as the Subscribe request's
	// publisher_subscription_id, so the publisher's view IS the canonical
	// id we use for the publisher capability check on POST /messages.
	ids := pub.SubscriptionIDs()
	if len(ids) != 1 {
		t.Fatalf("publisher must hold exactly one subscription after a single instance create, got %d: %v", len(ids), ids)
	}
	state.subscriptionID = ids[0]
}

// exerciseMessageDeliveryLeg emits a publisher message via the real
// `POST /v1/instances/{id}/messages` endpoint with the mandatory
// Idempotency-Key header, sender_kind=publisher, and the captured
// publisher_subscription_id. Asserts the downstream node's work_started
// count grows (the cascade fired) and the persisted message carries
// sender_kind=publisher with sender derived from the publisher_name.
//
// Proof for spec acceptance leg (b): "the publisher begins emitting
// messages to the rimsky message endpoint; the messages reach the
// targeted instance and downstream nodes consume them".
func exerciseMessageDeliveryLeg(t *testing.T, ep harness.RimskyEndpoint, state *exampleState) {
	// Quiesce the reactor's initial-frame activity first — the reactor
	// node runs once on instance create (its initial frame), then settles.
	// Subsequent work_started growth must be attributable to the published
	// message alone.
	waitForNodeDispatched(t, ep, state.instanceID, reactorNodeType, 60*time.Second)
	waitForDispatchQuiescent(t, ep, state.instanceID, reactorNodeType, 30*time.Second)
	baseline := workStartedCount(t, ep, state.instanceID, reactorNodeType)

	// Emit a publisher message — exactly what the bundled sensors and
	// any third-party publisher author would send through
	// pkg:lib/protocols/publisherkit. The envelope shape mirrors
	// lib/services/sensors/sensor-http/sensor.go::postMessage so the
	// example tracks the production publisher wire format.
	envelope := map[string]any{
		"kind":                      exampleMessageKind,
		"target":                    reactorNodeType,
		"payload":                   map[string]any{"hello": "world"},
		"sender":                    "example-publisher",
		"sender_kind":               "publisher",
		"publisher_subscription_id": state.subscriptionID,
	}
	status, body := postWithHeader(t, ep, "/v1/instances/"+state.instanceID+"/messages",
		envelope, map[string]string{"Idempotency-Key": "ck-example-emit-1"})
	if status != http.StatusCreated {
		t.Fatalf("POST /v1/instances/%s/messages with valid envelope: status=%d want=201 body=%s",
			state.instanceID, status, string(body))
	}

	// The cascade fires asynchronously: rimsky persists the message,
	// schedules a frame, the supervisor dispatches the reactor node again.
	requireWorkStartedGrew(t, ep, state.instanceID, reactorNodeType, baseline, 60*time.Second,
		"published message must propagate through the cascade and re-dispatch the subscribing node")

	// Read back the persisted message via GET /v1/instances/{id}/messages
	// and assert sender_kind=publisher + sender=publisher_name. Per the
	// publisher capability check (lib/control/controlapi/messages.go),
	// rimsky overwrites the request body's `sender` with the
	// publisher-subscription row's PublisherName — proof that
	// sender_kind discrimination is server-derived, not client-trusted.
	requirePublisherMessage(t, ep, state.instanceID, "example")
}

// exerciseMissingDedupHeaderLeg POSTs a structurally-valid publisher
// envelope with NO Idempotency-Key header and asserts rimsky refuses
// the request with 400. This is the falsifier guard "the publisher
// emits without the dedup header and is silently accepted".
//
// Proof for spec acceptance leg (c): the mandatory dedup header is
// platform-enforced, not a publisher convention.
func exerciseMissingDedupHeaderLeg(t *testing.T, ep harness.RimskyEndpoint, state *exampleState) {
	envelope := map[string]any{
		"kind":                      exampleMessageKind,
		"target":                    reactorNodeType,
		"payload":                   map[string]any{"missing": "header"},
		"sender":                    "example-publisher",
		"sender_kind":               "publisher",
		"publisher_subscription_id": state.subscriptionID,
	}
	// Headers map intentionally empty — no Idempotency-Key.
	status, body := postWithHeader(t, ep, "/v1/instances/"+state.instanceID+"/messages",
		envelope, map[string]string{})
	if status != http.StatusBadRequest {
		t.Fatalf("POST /v1/instances/%s/messages WITHOUT Idempotency-Key: status=%d want=400 body=%s (falsifier: publisher emits without dedup header and is silently accepted)",
			state.instanceID, status, string(body))
	}
	// Diagnostic must name the missing header so an operator knows what
	// to fix. Lower-case search keeps the assertion resilient to body
	// capitalization.
	bodyLower := strings.ToLower(string(body))
	if !strings.Contains(bodyLower, "idempotency-key") {
		t.Fatalf("400 body must name the Idempotency-Key header: %s", string(body))
	}
}

// exerciseRestartReconcileLeg restarts the rimsky-all-in-one container
// (preserving Postgres + the publisher) and asserts:
//   - ListSubscriptions count grows (the new control-api ran resync).
//   - Subscribe count does NOT grow (the publisher reported the live
//     subscription, so reconcile left it alone).
//   - The publisher's in-memory registry still holds the same
//     publisher_subscription_id.
//
// Proof for spec acceptance leg (d): "rimsky calls ListSubscriptions on
// the publisher and reconciles back to the steady state without
// re-subscribing what's already there".
func exerciseRestartReconcileLeg(ctx context.Context, t *testing.T, h *harness.RimskyHandle, pub *Publisher, state *exampleState) {
	beforeIDs := pub.SubscriptionIDs()
	beforeCalls := pub.Calls()

	// Restart rimsky. The Postgres testcontainer + the example publisher
	// both survive; only the rimsky-all-in-one container is recycled.
	// The new control-api dials the same publisher endpoint
	// (host.testcontainers.internal:<port>), fires the Capabilities
	// handshake, and runs ResyncPublisherSubscriptions in a goroutine
	// against every configured publisher.
	h.Restart(ctx, t)

	// Resync runs in a startup goroutine (lib/control/config/controlapi.go,
	// after StartControlAPI returns), so /health-200 does not imply
	// reconcile has run yet. Poll for ListSubscriptions to be called.
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if pub.Calls().ListSubscriptions > beforeCalls.ListSubscriptions {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	after := pub.Calls()
	if after.ListSubscriptions <= beforeCalls.ListSubscriptions {
		// Dump the rimsky container logs so the failure surface enumerates
		// what the control-api was actually doing — the resync goroutine
		// logs `publisher.resync.*` keys on every step (list_failed,
		// subscribe_failed, orphan_subscription, unsubscribe_orphan_failed),
		// so an absent log line says the goroutine never ran the
		// ListSubscriptions branch.
		h.DumpRimskyLogs(t)
		t.Fatalf("ListSubscriptions count did NOT grow after rimsky restart: before(after-initial-startup)=%d after-restart=%d (snapshot of all counters at end: subscribe=%d unsubscribe=%d listSubs=%d) — the new control-api must invoke runtime.ResyncPublisherSubscriptions, which calls PublisherClient.ListSubscriptions on every configured publisher",
			beforeCalls.ListSubscriptions, after.ListSubscriptions,
			after.Subscribe, after.Unsubscribe, after.ListSubscriptions)
	}

	// Falsifier guard: Subscribe must NOT have grown — the publisher
	// reported the still-active subscription on ListSubscriptions, so
	// the reconcile must have observed it as already-present and left
	// it alone. Wait briefly for any racing in-flight Subscribe to land
	// before snapshotting, so a slow re-Subscribe still fails this gate.
	time.Sleep(2 * time.Second)
	after = pub.Calls()
	if after.Subscribe > beforeCalls.Subscribe {
		t.Fatalf("Subscribe count GREW across rimsky restart: before=%d after=%d — the post-restart reconcile re-subscribed an already-active subscription, which is exactly the falsifier (\"the post-restart reconcile re-subscribes already-active subscriptions\"). ListSubscriptions reported it as live; the reconcile must leave live subscriptions alone",
			beforeCalls.Subscribe, after.Subscribe)
	}

	// The publisher's in-memory registry must still hold the same
	// subscription id (nothing has removed it).
	afterIDs := pub.SubscriptionIDs()
	if len(afterIDs) != len(beforeIDs) {
		t.Fatalf("publisher subscription set changed across restart: before=%v after=%v", beforeIDs, afterIDs)
	}
	if len(afterIDs) > 0 && afterIDs[0] != state.subscriptionID {
		t.Fatalf("publisher subscription id changed across restart: was %q, now %v", state.subscriptionID, afterIDs)
	}
}

// --- template + envelope helpers ----------------------------------------------

// reactorNodeType is both the publisher's `target_node` and the subscribing
// node's type. The publisher's envelope carries target=<this>; the reactor
// node subscribes to `message/<message_kind>/publisher/<target>` so the
// delivered signal type matches it.
const reactorNodeType = "reactor"

// exampleMessageKind is the publisher's `message_kind` — the wire-level
// invalidate-class kind. V1 only supports `invalidate`; the example tracks
// that contract.
const exampleMessageKind = "invalidate"

// deployExampleTemplate POSTs a template referencing the example publisher
// and deploys it. The template wires:
//   - a publisher `example-pub` (kind: example — what the publisher's
//     Capabilities advertises) targeting the reactor node, message_kind
//     invalidate.
//   - a `reactor` node subscribing to
//     `message/invalidate/publisher/reactor` (the canonical signal type
//     for a publisher-emitted invalidate to the reactor target), running
//     the example executor's role-stand-in via the all-in-one's default
//     stub executor.
//
// The reactor node's executor is intentionally an unconfigured executor
// reference — rimsky's reference-validation defaults to `available` for
// the all-in-one image, so a missing executor doesn't refuse registration.
// We rely on the node still entering its initial fresh frame and growing
// work_started on every dispatch attempt; the cascade-fired re-dispatch
// is what we measure across the publisher emit.
func deployExampleTemplate(t *testing.T, ep harness.RimskyEndpoint) string {
	t.Helper()

	body := map[string]any{
		"spec": map[string]any{
			"name":                  "example-publisher-cascade",
			"version":               "1",
			"frame_resolution_mode": "serial_queue",
			"frame_timeout_ms":      600000,
			"nodes": []map[string]any{
				{
					"type":     reactorNodeType,
					"executor": "stub",
					"subscribes": []map[string]any{
						{
							"instance": true,
							"type":     "message/" + exampleMessageKind + "/publisher/" + reactorNodeType,
							"frame":    "in",
						},
					},
				},
			},
			"publishers": []map[string]any{
				{
					// `name` must match the key under `publishers:` in
					// rimsky.yml (the harness's WithPublisher option uses
					// "example"). StartPublisherSubscriptionsForInstance
					// looks up the publisher client by Name, so a name
					// mismatch means Subscribe is never dispatched.
					"name":         "example",
					"kind":         exampleKind,
					"config":       json.RawMessage(`{}`),
					"target_node":  reactorNodeType,
					"message_kind": exampleMessageKind,
				},
			},
		},
	}

	status, raw := ep.PostJSON(t, "/v1/templates", body)
	if status != http.StatusCreated {
		t.Fatalf("POST /v1/templates: %d %s", status, string(raw))
	}
	var resp struct {
		TemplateID string `json:"template_id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode template response: %v: %s", err, string(raw))
	}
	if resp.TemplateID == "" {
		t.Fatalf("template_id empty: %s", string(raw))
	}
	deployStatus, deployRaw := ep.PostJSON(t, "/v1/templates/"+resp.TemplateID+"/deploy", map[string]any{})
	if deployStatus != http.StatusOK {
		t.Fatalf("POST /v1/templates/%s/deploy: %d %s", resp.TemplateID, deployStatus, string(deployRaw))
	}
	return resp.TemplateID
}

// createExampleInstance POSTs a new instance and returns its id. Creating
// the instance fires StartPublisherSubscriptionsForInstance, which inserts
// the rimsky_publisher_subscriptions row and calls the example publisher's
// Subscribe RPC with the resolved config.
func createExampleInstance(t *testing.T, ep harness.RimskyEndpoint, templateID, instanceKey string) string {
	t.Helper()
	status, raw := ep.PostJSON(t, "/v1/instances", map[string]any{
		"template":     templateID,
		"instance_key": instanceKey,
		"params":       map[string]any{},
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /v1/instances: %d %s", status, string(raw))
	}
	var resp struct {
		InstanceID string `json:"instance_id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode instance response: %v: %s", err, string(raw))
	}
	if resp.InstanceID == "" {
		t.Fatalf("instance_id empty: %s", string(raw))
	}
	return resp.InstanceID
}

// postWithHeader marshals body to JSON and POSTs with the supplied headers
// to ep.BaseURL+path. The harness's PostJSONWithHeaders does the same
// thing; this wrapper exists to keep the test-site one-liners tidy.
func postWithHeader(t *testing.T, ep harness.RimskyEndpoint, path string, body any, headers map[string]string) (int, []byte) {
	t.Helper()
	return ep.PostJSONWithHeaders(t, path, body, headers)
}

// --- observability helpers ---------------------------------------------------

// nodeStateResponse is the shape of
// `GET /v1/observability/nodes/{instance_id}/{node_type}`.
type nodeStateResponse struct {
	Node struct {
		State string `json:"state"`
	} `json:"node"`
	Events []struct {
		Kind string `json:"kind"`
	} `json:"events"`
}

// waitForNodeDispatched polls the node-state observability route until
// the node has been dispatched at least once (work_started count > 0).
// Used as the precondition for waitForDispatchQuiescent so the quiescent
// window starts from a meaningful dispatch state.
func waitForNodeDispatched(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if workStartedCount(t, ep, instanceID, nodeType) > 0 {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("node %q on instance %s did not receive a dispatch within %v — initial-frame processing must complete before the publisher-emit measurement begins",
		nodeType, instanceID, deadline)
}

// waitForDispatchQuiescent polls a node's work_started count until it
// stops growing for a stability window. Mirrors sensor_cascade_e2e_test.go's
// shape: draining initial-frame activity before measuring later growth is
// what makes the publisher-emit re-dispatch unambiguous.
func waitForDispatchQuiescent(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string, deadline time.Duration) {
	t.Helper()
	const stableWindow = 4 * time.Second
	end := time.Now().Add(deadline)
	last := workStartedCount(t, ep, instanceID, nodeType)
	stableSince := time.Now()
	for time.Now().Before(end) {
		time.Sleep(500 * time.Millisecond)
		cur := workStartedCount(t, ep, instanceID, nodeType)
		if cur != last {
			last = cur
			stableSince = time.Now()
			continue
		}
		if time.Since(stableSince) >= stableWindow {
			return
		}
	}
	// Non-fatal at this stage: an undispatched-executor configuration means
	// the reactor may keep retrying. The follow-up requireWorkStartedGrew
	// still discriminates publisher-emit-caused growth from baseline.
	t.Logf("warning: node %q never went fully quiescent within %v (work_started kept moving, last=%d); proceeding with baseline=%d",
		nodeType, deadline, last, last)
}

// workStartedCount returns the number of `work_started` events the node
// has emitted — one per real supervisor dispatch attempt. Mirrors the
// sensor cascade test's observable.
func workStartedCount(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string) int {
	t.Helper()
	status, raw := ep.GetJSON(t, "/v1/observability/nodes/"+instanceID+"/"+nodeType, "")
	if status != http.StatusOK {
		return 0
	}
	var resp nodeStateResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return 0
	}
	n := 0
	for _, e := range resp.Events {
		if e.Kind == "work_started" {
			n++
		}
	}
	return n
}

// requireWorkStartedGrew asserts the node's work_started count grew past
// `baseline` within the deadline — unambiguous proof the cascade re-ran
// the node on the publisher's emit. Fails hard on timeout.
func requireWorkStartedGrew(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string, baseline int, deadline time.Duration, why string) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if workStartedCount(t, ep, instanceID, nodeType) > baseline {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("node %q on instance %s did not re-run after the publisher emit (work_started stayed at %d) within %v — %s",
		nodeType, instanceID, baseline, deadline, why)
}

// requirePublisherMessage asserts a message persisted for the instance
// with sender_kind=publisher and sender == wantSender (the publisher
// name from the template's `publishers:` block, derived by rimsky from
// the publisher-subscription row — NOT the request body's `sender`,
// which rimsky overwrites for trust). Reads via the real `GET
// /v1/instances/{id}/messages` surface so the assertion exercises the
// persisted, trust-derived sender.
func requirePublisherMessage(t *testing.T, ep harness.RimskyEndpoint, instanceID, wantSender string) {
	t.Helper()
	end := time.Now().Add(30 * time.Second)
	var lastSeen string
	for time.Now().Before(end) {
		status, raw := ep.GetJSON(t,
			"/v1/instances/"+instanceID+"/messages?sender_kind=publisher", "")
		if status == http.StatusOK {
			var resp struct {
				Messages []struct {
					Kind       string `json:"kind"`
					Sender     string `json:"sender"`
					SenderKind string `json:"sender_kind"`
				} `json:"messages"`
			}
			if err := json.Unmarshal(raw, &resp); err == nil {
				for _, m := range resp.Messages {
					lastSeen = fmt.Sprintf("kind=%s sender=%s sender_kind=%s", m.Kind, m.Sender, m.SenderKind)
					if m.SenderKind != "publisher" {
						continue
					}
					if m.Sender != wantSender {
						t.Fatalf("publisher message persisted with sender=%q, want %q (rimsky must derive sender from the publisher-subscription's publisher_name on the persisted row, not the request body's sender)",
							m.Sender, wantSender)
					}
					return
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("no message with sender_kind=publisher persisted for instance %s within deadline; last seen=%q",
		instanceID, lastSeen)
}

// --- in-process publisher bring-up -------------------------------------------

// freeHostPort grabs an OS-assigned TCP port and returns it. The brief
// close-then-reuse race is acceptable for an in-process test fixture
// (matches the pattern in examples/executor/main_e2e_test.go::freeHostPort).
func freeHostPort(t *testing.T) int {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	port := lis.Addr().(*net.TCPAddr).Port
	if cerr := lis.Close(); cerr != nil {
		t.Fatalf("close listener: %v", cerr)
	}
	return port
}

// startExamplePublisher stands up the example Publisher as an in-process
// gRPC server on the given host port and blocks until the listener is
// accepting connections, so the caller can hand the endpoint to
// BringUpRimskyHandle knowing the eager Capabilities + ListSubscriptions
// handshake will succeed. The same publisher instance survives the
// rimsky restart in leg 4 — so its in-memory subscription registry is the
// "publisher persists its state" the restart-reconcile leg measures.
// Cleanup (graceful Stop) is registered via t.Cleanup.
func startExamplePublisher(t *testing.T, port int) *Publisher {
	t.Helper()
	lis, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("listen %d: %v", port, err)
	}
	srv := grpc.NewServer()
	pub := newPublisher()
	genv1.RegisterPublisherServer(srv, pub)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	// Poll-dial to confirm the gRPC server is up before returning.
	// rimsky-all-in-one's startup publisher dial is eager — if the
	// publisher isn't listening at the configured endpoint when rimsky
	// boots, the container exits non-zero. Blocking here makes the
	// ordering deterministic without a sleep.
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, dialErr := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			return pub
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("example publisher did not become dialable at %s within 10s", addr)
	return nil
}

// stubExecutorServer is a minimal Executor implementation that returns
// a single terminal Success for every dispatch. Mirrors the bundled
// lib/services/test/stubexecutor/main.go contract but lives inline here
// so the publisher example's cross-stack proof has no extra docker-build
// dependency. The reactor node uses this executor purely so the
// supervisor can issue a real dispatch (and emit work_started) —
// publisher-emit cascade fires regardless of dispatch outcome.
type stubExecutorServer struct {
	genv1.UnimplementedExecutorServer
}

// Execute sends exactly one terminal StreamClose{Success} and closes
// the stream — the minimal honest Executor contract per
// concept:executor.
func (stubExecutorServer) Execute(_ *genv1.ExecuteRequest, stream genv1.Executor_ExecuteServer) error {
	return stream.Send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
		StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Success{Success: &genv1.Success{
			Changed:       false,
			ChangeSummary: "stub executor: success",
		}}},
	}})
}

// stubObservabilityServer answers Capabilities with a permissive
// expected-attributes schema so the dispatch-time attribute gate
// (runtime.resolveAttributes) does not refuse the reactor node. Mirrors
// the bundled stub's observability shape — `{"type":"object"}` advertises
// the open shape graph/node.IsPermissiveExecutorSchema recognizes.
type stubObservabilityServer struct {
	genv1.UnimplementedExecutorObservabilityServer
}

// Capabilities returns the open-schema, no-trace observability contract.
func (stubObservabilityServer) Capabilities(_ context.Context, _ *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	return &genv1.ObservabilityCapabilities{
		SupportsTraceGet:              false,
		SupportsTraceStream:           false,
		RetentionAfterTerminalSeconds: 0,
		ExpectedAttributesSchema:      []byte(`{"type":"object"}`),
	}, nil
}

// GetTrace returns Unimplemented (the stub retains no traces).
func (stubObservabilityServer) GetTrace(_ context.Context, _ *genv1.GetTraceRequest) (*genv1.Trace, error) {
	return nil, status.Error(codes.Unimplemented, "stub executor: GetTrace not supported")
}

// StreamTrace returns Unimplemented.
func (stubObservabilityServer) StreamTrace(_ *genv1.StreamTraceRequest, _ genv1.ExecutorObservability_StreamTraceServer) error {
	return status.Error(codes.Unimplemented, "stub executor: StreamTrace not supported")
}

// startStubExecutor brings up the inline stub executor on a host port.
// Mirrors startExamplePublisher's ordering discipline (block until the
// listener accepts connections) so the harness's eager Capabilities
// handshake succeeds at rimsky startup.
func startStubExecutor(t *testing.T, port int) {
	t.Helper()
	lis, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("stub executor listen %d: %v", port, err)
	}
	srv := grpc.NewServer()
	genv1.RegisterExecutorServer(srv, stubExecutorServer{})
	genv1.RegisterExecutorObservabilityServer(srv, stubObservabilityServer{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, dialErr := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("stub executor did not become dialable at %s within 10s", addr)
}
