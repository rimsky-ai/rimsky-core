// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Cross-stack proof for STORY-publisher-protocol: a service author's
// example Publisher — registered with rimsky's publisher catalog,
// advertising the kinds it emits via Capabilities, handling Subscribe /
// Unsubscribe / ListSubscriptions — plugs into a running rimsky stack
// end-to-end through the public protocol surface. The four legs
// exhibit:
//
//  1. Subscribe lands when an instance is created against a template
//     whose `publishers:` block names the example publisher's kind.
//  2. The publisher emits a message via the universal route
//     `POST /v1/instances/{id}/messages` with the mandatory
//     `Idempotency-Key` header, `sender_kind=publisher`, and the
//     `publisher_subscription_id` capability token. The downstream
//     node subscribing to the message-virtual-node fires through the
//     real cascade.
//  3. The Idempotency-Key header is mandatory — a POST without it is
//     refused with 400 at the request boundary.
//  4. Restart-time reconcile uses ListSubscriptions and does NOT
//     re-Subscribe an already-active subscription.
//
// Per TD-execute-rpc-unary the stub executor used by the worker node
// returns a settling Outcome directly (no stream, no heartbeats, no
// named events).
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
// fetched from a registry.
func TestE2E_ExamplePublisherAgainstRunningRimsky(t *testing.T) {
	ctx := context.Background()

	pubPort := freeHostPort(t)
	pub := startExamplePublisher(t, pubPort)

	execPort := freeHostPort(t)
	startStubExecutor(t, execPort)

	pubEndpoint := fmt.Sprintf("host.testcontainers.internal:%d", pubPort)
	execEndpoint := fmt.Sprintf("host.testcontainers.internal:%d", execPort)
	h := harness.BringUpRimskyHandle(ctx, t,
		harness.WithPublisher("example", pubEndpoint),
		harness.WithExecutor("stub", execEndpoint),
		harness.WithHostPortAccess(pubPort, execPort),
		harness.WithRefValidationMode("none"),
	)

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
	t.Run("Legacy_observations_route_is_gone", func(t *testing.T) {
		exerciseLegacyRouteGoneLeg(t, h.Endpoint, state)
	})
	t.Run("Restart_reconcile_uses_ListSubscriptions_without_resubscribing", func(t *testing.T) {
		exerciseRestartReconcileLeg(ctx, t, h, pub, state)
	})
}

// exampleState carries the IDs created in leg 1 and reused by legs 2/3/4.
type exampleState struct {
	templateID     string
	instanceID     string
	subscriptionID string
}

// exerciseSubscribeLeg deploys a template referencing the example
// publisher's kind, creates an instance, and asserts the publisher's
// Subscribe handler was invoked exactly once with a matching
// publisher_subscription_id.
func exerciseSubscribeLeg(t *testing.T, ep harness.RimskyEndpoint, pub *Publisher, state *exampleState) {
	// @deliberate: wait briefly for the startup resync goroutine to
	// run. Resync is invoked from a `go func()` after StartControlAPI
	// returns, so /health-200 does not imply it has executed yet.
	startupDeadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(startupDeadline) {
		if pub.Calls().ListSubscriptions > 0 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if pub.Calls().ListSubscriptions == 0 {
		t.Fatalf("initial-startup ResyncPublisherSubscriptions never called PublisherClient.ListSubscriptions on the example publisher within 30s")
	}

	before := pub.Calls()
	state.templateID = deployExampleTemplate(t, ep)
	state.instanceID = createExampleInstance(t, ep, state.templateID, "ck-example-publisher")

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if pub.Calls().Subscribe > before.Subscribe {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	after := pub.Calls()
	if after.Subscribe <= before.Subscribe {
		t.Fatalf("Subscribe count did NOT grow on instance create: before=%d after=%d "+
			"(rimsky must call PublisherClient.Subscribe synchronously on instance-create)",
			before.Subscribe, after.Subscribe)
	}

	ids := pub.SubscriptionIDs()
	if len(ids) != 1 {
		t.Fatalf("publisher must hold exactly one subscription after one instance create, got %d: %v", len(ids), ids)
	}
	state.subscriptionID = ids[0]
}

// exerciseMessageDeliveryLeg emits a publisher message via the real
// `POST /v1/instances/{id}/messages` endpoint with the mandatory
// Idempotency-Key header, sender_kind=publisher, and the captured
// publisher_subscription_id. Asserts the downstream node's work_started
// count grows (the cascade fired) and the persisted message carries
// sender_kind=publisher with sender derived from the publisher_name.
func exerciseMessageDeliveryLeg(t *testing.T, ep harness.RimskyEndpoint, state *exampleState) {
	baseline := workStartedCount(t, ep, state.instanceID, reactorNodeType)

	envelope := map[string]any{
		"type":                      exampleMessageType,
		"payload":                   map[string]any{"hello": "world"},
		"sender":                    "example-publisher",
		"sender_kind":               "publisher",
		"publisher_subscription_id": state.subscriptionID,
	}
	statusCode, body := postWithHeader(t, ep, "/v1/instances/"+state.instanceID+"/messages",
		envelope, map[string]string{"Idempotency-Key": "ck-example-emit-1"})
	if statusCode != http.StatusCreated {
		t.Fatalf("POST /v1/instances/%s/messages: status=%d want=201 body=%s",
			state.instanceID, statusCode, string(body))
	}

	requireWorkStartedGrew(t, ep, state.instanceID, reactorNodeType, baseline, 60*time.Second,
		"published message must propagate through the cascade and re-dispatch the subscribing node")

	requirePublisherMessage(t, ep, state.instanceID, "example")

	// @constraint: replay with the same Idempotency-Key returns 200 OK
	// with the original message_id (dedup contract per
	// concept:message-idempotency).
	replayStatus, replayBody := postWithHeader(t, ep, "/v1/instances/"+state.instanceID+"/messages",
		envelope, map[string]string{"Idempotency-Key": "ck-example-emit-1"})
	if replayStatus != http.StatusOK {
		t.Fatalf("Idempotency-Key replay: status=%d want=200 (dedup must return 200, not a fresh 201)",
			replayStatus)
	}
	// @constraint: a fresh Idempotency-Key returns 201 Created with a
	// new message_id (no dedup).
	freshEnvelope := map[string]any{
		"type":                      exampleMessageType,
		"payload":                   map[string]any{"hello": "world-2"},
		"sender":                    "example-publisher",
		"sender_kind":               "publisher",
		"publisher_subscription_id": state.subscriptionID,
	}
	freshStatus, freshBody := postWithHeader(t, ep, "/v1/instances/"+state.instanceID+"/messages",
		freshEnvelope, map[string]string{"Idempotency-Key": "ck-example-emit-2"})
	if freshStatus != http.StatusCreated {
		t.Fatalf("fresh Idempotency-Key: status=%d want=201; replay_body=%s fresh_body=%s",
			freshStatus, string(replayBody), string(freshBody))
	}
}

// exerciseMissingDedupHeaderLeg POSTs a structurally-valid publisher
// envelope with NO Idempotency-Key header and asserts rimsky refuses
// the request with 400.
func exerciseMissingDedupHeaderLeg(t *testing.T, ep harness.RimskyEndpoint, state *exampleState) {
	envelope := map[string]any{
		"type":                      exampleMessageType,
		"payload":                   map[string]any{"missing": "header"},
		"sender":                    "example-publisher",
		"sender_kind":               "publisher",
		"publisher_subscription_id": state.subscriptionID,
	}
	statusCode, body := postWithHeader(t, ep, "/v1/instances/"+state.instanceID+"/messages",
		envelope, map[string]string{})
	if statusCode != http.StatusBadRequest {
		t.Fatalf("POST /v1/instances/%s/messages WITHOUT Idempotency-Key: status=%d want=400 body=%s",
			state.instanceID, statusCode, string(body))
	}
	bodyLower := strings.ToLower(string(body))
	if !strings.Contains(bodyLower, "idempotency-key") {
		t.Fatalf("400 body must name the Idempotency-Key header: %s", string(body))
	}
}

// exerciseLegacyRouteGoneLeg pins that the pre-coherence sensor route
// `POST /sensors/{watch_id}/observations` returns 404 — the universal
// `POST /v1/instances/{id}/messages` endpoint is the only message
// intake under the 2026-05-17 publisher-protocol unification.
func exerciseLegacyRouteGoneLeg(t *testing.T, ep harness.RimskyEndpoint, state *exampleState) {
	req, err := http.NewRequest(http.MethodPost,
		ep.BaseURL+"/v1/sensors/"+state.subscriptionID+"/observations",
		strings.NewReader(`{"payload":{}}`))
	if err != nil {
		t.Fatalf("build POST: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST legacy route: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("legacy /v1/sensors/{watch_id}/observations route: status=%d want=404 "+
			"(the route was retired by the 2026-05-17 publisher-protocol unification)", resp.StatusCode)
	}
}

// exerciseRestartReconcileLeg restarts the rimsky-all-in-one container
// (preserving Postgres + the publisher) and asserts:
//   - ListSubscriptions count grows (the new control-api ran resync).
//   - Subscribe count does NOT grow (the publisher reported the live
//     subscription, so reconcile left it alone).
//   - The publisher's in-memory registry still holds the same
//     publisher_subscription_id.
func exerciseRestartReconcileLeg(ctx context.Context, t *testing.T, h *harness.RimskyHandle, pub *Publisher, state *exampleState) {
	beforeIDs := pub.SubscriptionIDs()
	beforeCalls := pub.Calls()

	h.Restart(ctx, t)

	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if pub.Calls().ListSubscriptions > beforeCalls.ListSubscriptions {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	after := pub.Calls()
	if after.ListSubscriptions <= beforeCalls.ListSubscriptions {
		h.DumpRimskyLogs(t)
		t.Fatalf("ListSubscriptions did NOT grow after rimsky restart: before=%d after=%d",
			beforeCalls.ListSubscriptions, after.ListSubscriptions)
	}

	time.Sleep(2 * time.Second)
	after = pub.Calls()
	if after.Subscribe > beforeCalls.Subscribe {
		t.Fatalf("Subscribe count GREW across rimsky restart: before=%d after=%d "+
			"(the falsifier names re-Subscribing live subscriptions)",
			beforeCalls.Subscribe, after.Subscribe)
	}

	afterIDs := pub.SubscriptionIDs()
	if len(afterIDs) != len(beforeIDs) {
		t.Fatalf("publisher subscription set changed across restart: before=%v after=%v", beforeIDs, afterIDs)
	}
	if len(afterIDs) > 0 && afterIDs[0] != state.subscriptionID {
		t.Fatalf("publisher subscription id changed across restart: was %q, now %v", state.subscriptionID, afterIDs)
	}
}

// reactorNodeType is the subscribing node's type. The publisher's
// envelope carries type=<exampleMessageType>, declared in the
// template's `messages:` registry as a virtual node-type the reactor
// subscribes to via the message-schema-layer DSL.
const reactorNodeType = "reactor"

// exampleMessageType is the template-declared message type the
// publisher emits; the reactor subscribes through the message-schema-
// layer DSL.
const exampleMessageType = "invalidate/reactor"

// deployExampleTemplate POSTs a template wiring a reactor node that
// subscribes to a message-virtual-node, with `example` declared as the
// publisher kind. Returns the template id.
func deployExampleTemplate(t *testing.T, ep harness.RimskyEndpoint) string {
	t.Helper()
	body := map[string]any{
		"spec": map[string]any{
			"name":             "example-publisher-cascade",
			"version":          "1",
			"frame_timeout_ms": 600000,
			"messages": []map[string]any{
				{
					"type": exampleMessageType,
					"body_schema": map[string]any{
						"type":                 "object",
						"properties":           map[string]any{},
						"additionalProperties": true,
					},
				},
			},
			"nodes": []map[string]any{
				{
					"type":     reactorNodeType,
					"executor": "stub",
					"subscribes": []map[string]any{
						{
							"node":                   exampleMessageType,
							"type":                   "terminal/success",
							"wake_on_change":         true,
							"force_upstream_refresh": false,
						},
					},
				},
			},
			"publishers": []map[string]any{
				{
					"name":         "example",
					"kind":         exampleKind,
					"config":       json.RawMessage(`{}`),
					"target_node":  reactorNodeType,
					"message_type": exampleMessageType,
				},
			},
		},
	}

	statusCode, raw := ep.PostJSON(t, "/v1/templates", body)
	if statusCode != http.StatusCreated {
		t.Fatalf("POST /v1/templates: %d %s", statusCode, string(raw))
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

// createExampleInstance POSTs a new instance and returns its id.
func createExampleInstance(t *testing.T, ep harness.RimskyEndpoint, templateID, instanceKey string) string {
	t.Helper()
	statusCode, raw := ep.PostJSON(t, "/v1/instances", map[string]any{
		"template":     templateID,
		"instance_key": instanceKey,
		"params":       map[string]any{},
	})
	if statusCode != http.StatusCreated {
		t.Fatalf("POST /v1/instances: %d %s", statusCode, string(raw))
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

// postWithHeader marshals body to JSON and POSTs with the supplied
// headers to ep.BaseURL+path.
func postWithHeader(t *testing.T, ep harness.RimskyEndpoint, path string, body any, headers map[string]string) (int, []byte) {
	t.Helper()
	return ep.PostJSONWithHeaders(t, path, body, headers)
}

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

// workStartedCount returns the number of `work_started` events the
// node has emitted — one per real supervisor dispatch attempt.
func workStartedCount(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string) int {
	t.Helper()
	statusCode, raw := ep.GetJSON(t, "/v1/observability/nodes/"+instanceID+"/"+nodeType, "")
	if statusCode != http.StatusOK {
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

// requireWorkStartedGrew asserts the node's work_started count grew
// past `baseline` within the deadline.
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
// with sender_kind=publisher and sender == wantSender.
func requirePublisherMessage(t *testing.T, ep harness.RimskyEndpoint, instanceID, wantSender string) {
	t.Helper()
	end := time.Now().Add(30 * time.Second)
	var lastSeen string
	for time.Now().Before(end) {
		statusCode, raw := ep.GetJSON(t,
			"/v1/instances/"+instanceID+"/messages?sender_kind=publisher", "")
		if statusCode == http.StatusOK {
			var resp struct {
				Messages []struct {
					Type       string `json:"type"`
					Sender     string `json:"sender"`
					SenderKind string `json:"sender_kind"`
				} `json:"messages"`
			}
			if err := json.Unmarshal(raw, &resp); err == nil {
				for _, m := range resp.Messages {
					lastSeen = fmt.Sprintf("type=%s sender=%s sender_kind=%s", m.Type, m.Sender, m.SenderKind)
					if m.SenderKind != "publisher" {
						continue
					}
					if m.Sender != wantSender {
						t.Fatalf("publisher message persisted with sender=%q, want %q",
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

// freeHostPort grabs an OS-assigned TCP port and returns it.
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
// accepting connections.
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

// stubExecutorServer implements the unary Executor.Execute returning a
// Success Outcome (per TD-execute-rpc-unary). Mirrors the bundled
// test/support/executors/stub/ contract — kept inline so the example's
// cross-stack proof has no extra docker-build dependency.
type stubExecutorServer struct {
	genv1.UnimplementedExecutorServer
}

// Execute returns a single settling Success Outcome (no stream).
func (stubExecutorServer) Execute(_ context.Context, _ *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
		Changed:       false,
		ChangeSummary: "stub executor: success",
	}}}, nil
}

// stubObservabilityServer answers Capabilities with a permissive
// expected-attributes schema so the dispatch-time attribute gate does
// not refuse the reactor node.
type stubObservabilityServer struct {
	genv1.UnimplementedExecutorObservabilityServer
}

// Capabilities returns the open-schema, no-trace observability
// contract.
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

// StreamTrace returns Unimplemented (the stub retains no traces).
func (stubObservabilityServer) StreamTrace(_ *genv1.StreamTraceRequest, _ genv1.ExecutorObservability_StreamTraceServer) error {
	return status.Error(codes.Unimplemented, "stub executor: StreamTrace not supported")
}

// startStubExecutor brings up the inline stub executor on a host port.
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
