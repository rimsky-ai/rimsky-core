// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

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

type exampleState struct {
	templateID     string
	instanceID     string
	subscriptionID string
}

func exerciseSubscribeLeg(t *testing.T, ep harness.RimskyEndpoint, pub *Publisher, state *exampleState) {
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

func exerciseMessageDeliveryLeg(t *testing.T, ep harness.RimskyEndpoint, state *exampleState) {
	baseline := workStartedCount(t, ep, state.instanceID, reactorNodeType)

	envelope := map[string]any{
		"type":                      exampleMessageType,
		"payload":                   map[string]any{"hello": "world"},
		"sender":                    "example-publisher",
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

	replayStatus, replayBody := postWithHeader(t, ep, "/v1/instances/"+state.instanceID+"/messages",
		envelope, map[string]string{"Idempotency-Key": "ck-example-emit-1"})
	if replayStatus != http.StatusOK {
		t.Fatalf("Idempotency-Key replay: status=%d want=200 (dedup must return 200, not a fresh 201)",
			replayStatus)
	}
	freshEnvelope := map[string]any{
		"type":                      exampleMessageType,
		"payload":                   map[string]any{"hello": "world-2"},
		"sender":                    "example-publisher",
		"publisher_subscription_id": state.subscriptionID,
	}
	freshStatus, freshBody := postWithHeader(t, ep, "/v1/instances/"+state.instanceID+"/messages",
		freshEnvelope, map[string]string{"Idempotency-Key": "ck-example-emit-2"})
	if freshStatus != http.StatusCreated {
		t.Fatalf("fresh Idempotency-Key: status=%d want=201; replay_body=%s fresh_body=%s",
			freshStatus, string(replayBody), string(freshBody))
	}
}

func exerciseMissingDedupHeaderLeg(t *testing.T, ep harness.RimskyEndpoint, state *exampleState) {
	envelope := map[string]any{
		"type":                      exampleMessageType,
		"payload":                   map[string]any{"missing": "header"},
		"sender":                    "example-publisher",
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

const reactorNodeType = "reactor"

const exampleMessageType = "invalidate/reactor"

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

func postWithHeader(t *testing.T, ep harness.RimskyEndpoint, path string, body any, headers map[string]string) (int, []byte) {
	t.Helper()
	return ep.PostJSONWithHeaders(t, path, body, headers)
}

type nodeStateResponse struct {
	Events []struct {
		Kind string `json:"kind"`
	} `json:"events"`
}

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

type stubExecutorServer struct {
	genv1.UnimplementedExecutorServer
}

func (stubExecutorServer) Execute(_ context.Context, _ *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{
		Changed:       false,
		ChangeSummary: "stub executor: success",
	}}}, nil
}

type stubObservabilityServer struct {
	genv1.UnimplementedExecutorObservabilityServer
}

func (stubObservabilityServer) Capabilities(_ context.Context, _ *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	return &genv1.ObservabilityCapabilities{
		SupportsTraceGet:              false,
		SupportsTraceStream:           false,
		RetentionAfterTerminalSeconds: 0,
		ExpectedAttributesSchema:      []byte(`{"type":"object"}`),
	}, nil
}

func (stubObservabilityServer) GetTrace(_ context.Context, _ *genv1.GetTraceRequest) (*genv1.Trace, error) {
	return nil, status.Error(codes.Unimplemented, "stub executor: GetTrace not supported")
}

func (stubObservabilityServer) StreamTrace(_ *genv1.StreamTraceRequest, _ genv1.ExecutorObservability_StreamTraceServer) error {
	return status.Error(codes.Unimplemented, "stub executor: StreamTrace not supported")
}

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
