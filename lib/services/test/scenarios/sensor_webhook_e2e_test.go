// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// sensor_webhook_e2e_test.go is the cross-stack proof for
// STORY-sensor-webhook: an operator wiring an inbound-webhook-driven
// message into a workflow uses the bundled sensor-webhook to expose
// configured HTTP routes; inbound POSTs to a route translate to
// messages routed into rimsky against the subscription's target
// instance, with the request body translated into the message payload,
// and the inbound request is acknowledged only AFTER rimsky has
// persisted the message.
//
// The Falsifier brief for this story is:
//
//   - Inbound POST acknowledged before the message is persisted in
//     rimsky → refuted by reading the inbound response (200) and
//     IMMEDIATELY GET-ing /v1/instances/{id}/messages?sender_kind=publisher.
//     The persisted message MUST be present on that first read, with no
//     polling allowed: a sensor that returned 200 to the caller before
//     waiting for rimsky's POST /instances/{id}/messages to return would
//     race here — the message would arrive at rimsky only after the
//     caller has already proceeded.
//
//   - The path-prefix filter is declared but unused → refuted by
//     attempting a POST to a path OUTSIDE the configured `path_prefix`.
//     The sensor MUST return 404 and no message must persist in rimsky.
//     A sensor that ignored the prefix (e.g. accepted any POST) would
//     produce either a 200 ack or a stray persisted message.
//
//   - The request body translation is canned → refuted by POSTing a
//     real JSON body with a distinctive nested payload, then asserting
//     the persisted message's payload reflects the EXACT inbound bytes
//     end to end (including the nested object the test wrote and the
//     concrete URL path). A sensor that emitted a canned payload would
//     not surface the test's distinctive marker into the persisted
//     message row.
//
// The test boots a real rimsky-all-in-one against a real Postgres,
// brings up a real rimsky-sensor-webhook container as a peer, deploys a
// template declaring a webhook publisher with a `path_prefix`, drives a
// real HTTP POST to the sensor's host-mapped inbound port, and
// exercises all three falsifier prongs against the real assembled
// product end to end.
//
// @concept: sensor
// @concept: publisher
// @concept: publisher-subscription
// @story: sensor-webhook
package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

// webhookPublisherName is both the rimsky.yml publisher key (the name
// rimsky uses to dial the sensor peer) and the template's
// publishers[].name (the row in rimsky_publisher_subscriptions that
// rimsky derives `sender` from on POST /instances/{id}/messages).
const webhookPublisherName = "intake"

// webhookReactorNode is the template's reactor node — the
// publisher_subscription's target_node. The sensor's emitted envelope
// carries type=<webhookMessageType>, which is declared in the template's
// `messages:` registry as a virtual node-type the reactor subscribes to
// via `node: <webhookMessageType>, type: terminal/success`.
const webhookReactorNode = "reactor"

// webhookMessageType is the message type the webhook publisher emits,
// declared in the template's `messages:` registry. The reactor
// subscribes to it as a virtual node-type with terminal/success per
// the post-2026-06-14 message-schema-layer DSL.
const webhookMessageType = "invalidate/reactor"

// webhookPathPrefix is the per-subscription path the sensor mounts on
// its inbound HTTP server. POSTs UNDER this path translate to messages;
// POSTs outside it return 404. This is the load-bearing piece for the
// falsifier's "path-prefix filter declared but unused" prong.
const webhookPathPrefix = "/wh/ingest"

// TestSensorWebhook_InboundPostPersistsBeforeAck is the cross-stack
// STORY-sensor-webhook acceptance proof. See the file doc for the
// falsifier argument the test refutes.
func TestSensorWebhook_InboundPostPersistsBeforeAck(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.NewNetwork(ctx, t)

	// @constraint: bring up the sensor BEFORE rimsky so rimsky's eager-Dial
	// of the declared publisher at startup succeeds. The sensor sits on
	// the network at alias `sensor-webhook` with its inbound HTTP port
	// mapped to the host so the test can drive real POSTs at it.
	sensor := harness.StartSensorWebhook(ctx, t, netName, "sensor-webhook")

	// @deliberate: a stub executor so the reactor node has somewhere to
	// dispatch when an invalidate message lands. The reactor's actual run
	// is not under test here — the proof axis is the PERSISTED publisher
	// message in rimsky — but the executor declaration keeps registration
	// on the strict (default) ref_validation_mode.
	execEP := harness.StartExecutorStubOnNetwork(ctx, t, netName, "exec-ok")

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("stub", execEP),
		harness.WithPublisher(webhookPublisherName, sensor.GRPCEndpoint),
	)

	templateID := deploySensorWebhookTemplate(t, ep)
	instanceID := createSensorWebhookInstance(t, ep, templateID, "ck-sensor-webhook-e2e")

	// @constraint: wait on OBSERVABLE subscription state — mounting is
	// asynchronous (instance-create returns 201 with the row in
	// `mounting`; the reconciler drives Subscribe to `active`), so the
	// instance surface, not a wall-clock budget, says when the Subscribe
	// handshake has landed on the sensor (mounting the path in
	// sensor-webhook's pathToWatch).
	ep.WaitForSubscriptionsActive(t, instanceID, 90*time.Second)

	// @constraint: confirm sensor-side liveness — until Subscribe lands,
	// the catch-all dispatcher returns 404 for any path; polling for the
	// first 200 on the configured path is observable evidence the
	// subscription is live on the sensor itself.
	waitForWebhookSubscriptionActive(t, sensor.WebhookBaseURL, webhookPathPrefix, 30*time.Second)

	// @constraint: PROOF — path-prefix filter is honored (Falsifier prong
	// 2). POST to a path OUTSIDE the configured `path_prefix`. The
	// sensor's dispatcher MUST return 404 ("no active sensor-webhook
	// subscription for this path"). No message must persist in rimsky.
	preCount := publisherMessageCount(t, ep, instanceID)
	outsideStatus, outsideBody := postWebhook(t, sensor.WebhookBaseURL+"/wh/unrelated",
		[]byte(`{"should":"not-route"}`))
	if outsideStatus != http.StatusNotFound {
		t.Fatalf("POST to path OUTSIDE prefix returned %d (want 404), body=%q — the "+
			"path-prefix filter is declared but unused; any POST is being routed",
			outsideStatus, string(outsideBody))
	}
	// @constraint: no message must have landed — read once, no polling, so
	// a sensor that accepted the off-prefix POST and async-emitted would
	// also be caught after a short stability window.
	requirePublisherMessageCountStable(t, ep, instanceID, preCount, 2*time.Second,
		"off-prefix-post-must-not-emit")

	// @constraint: PROOF — inbound body is acknowledged ONLY after rimsky
	// has persisted the message (Falsifier prong 1) AND the persisted
	// payload reflects the real inbound bytes (Falsifier prong 3). The
	// inbound body carries a distinctive marker the test will read back
	// from the persisted message row — a canned payload would not surface
	// this marker. The body shape is intentionally non-trivial (nested
	// object + scalar + array) to make a canned-payload defect obvious.
	inboundBody := map[string]any{
		"event": "deploy.requested",
		"id":    "ck-sensor-webhook-payload-marker",
		"details": map[string]any{
			"requester": "ops",
			"reason":    "rollout-window",
			"ttl_ms":    float64(60000),
		},
		"labels": []any{"production", "us-east"},
	}
	rawBody, err := json.Marshal(inboundBody)
	if err != nil {
		t.Fatalf("marshal inbound body: %v", err)
	}

	// @constraint: snapshot the persisted count BEFORE the inbound POST so
	// the post-ack read can prove the count grew to 1 immediately,
	// without any polling. If the sensor ack'd before rimsky persisted,
	// the count would still be at preCount on that first read.
	beforePost := publisherMessageCount(t, ep, instanceID)

	postPath := sensor.WebhookBaseURL + webhookPathPrefix
	status, ackBody := postWebhook(t, postPath, rawBody)
	if status != http.StatusOK {
		t.Fatalf("inbound POST to %s returned %d (want 200), body=%q — the sensor "+
			"did not accept the inbound POST on the configured path; the path-prefix "+
			"mount did not register", postPath, status, string(ackBody))
	}

	// @constraint: read the persisted message NOW, no sleeps, no polling.
	// The sensor's serveWebhook is synchronous against postMessage, which
	// calls publisherkit.Send (blocking until rimsky returns); the 200
	// ack MUST follow the persistence. A non-synchronous sensor would
	// race here — the message would not be visible on the first read.
	immediateCount := publisherMessageCount(t, ep, instanceID)
	if immediateCount != beforePost+1 {
		t.Fatalf("persisted publisher message count was %d immediately after the inbound "+
			"200 ack (want %d) — the sensor acknowledged the inbound POST BEFORE rimsky "+
			"persisted the message; serveWebhook must block on postMessage",
			immediateCount, beforePost+1)
	}

	// @constraint: PROOF — persisted payload reflects the real inbound
	// bytes (Falsifier prong 3).
	persisted := readSinglePublisherMessage(t, ep, instanceID, webhookPublisherName)
	requirePersistedWebhookPayload(t, persisted, inboundBody, webhookPathPrefix)
}

// postWebhook drives a single HTTP POST against the sensor's host-
// mapped inbound listener and returns the status code and response
// body. The Content-Type is set to application/json so the sensor's
// best-effort JSON decode of the body lands the parsed object into the
// envelope payload (rather than the raw string fallback).
func postWebhook(t *testing.T, url string, body []byte) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build POST %s: %v", url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

// waitForWebhookSubscriptionActive polls the sensor's inbound HTTP
// server with HEAD-like probe POSTs until the configured path stops
// returning 404 — observable evidence that rimsky's Subscribe RPC has
// landed at the sensor and mounted the path in pathToWatch. Used to
// avoid racing the inbound proof against subscription registration.
// The probe POSTs carry a body distinct from the real inbound proof so
// any pre-mount 404s the wait absorbed are unambiguously NOT the proof
// payload.
//
// Note: a successful probe POST DOES emit a message into rimsky. The
// test accounts for this by snapshotting the message count immediately
// before the real inbound POST (beforePost) and asserting count growth
// from THAT baseline, not from zero.
func waitForWebhookSubscriptionActive(t *testing.T, baseURL, pathPrefix string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	var lastStatus int
	for time.Now().Before(end) {
		req, err := http.NewRequest(http.MethodPost, baseURL+pathPrefix,
			bytes.NewReader([]byte(`{"probe":"subscription-active"}`)))
		if err != nil {
			t.Fatalf("build probe POST: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			lastStatus = resp.StatusCode
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("sensor-webhook subscription did not become active on %s within %v "+
		"(last status=%d) — Subscribe handshake from rimsky did not mount the path",
		pathPrefix, deadline, lastStatus)
}

// readSinglePublisherMessage returns the single publisher-message row
// persisted for the instance with sender matching wantSender. Fails
// hard if zero or more than one matches — the test asserts an exact
// count so an off-prefix or duplicate emit would surface here.
func readSinglePublisherMessage(t *testing.T, ep harness.RimskyEndpoint, instanceID, wantSender string) map[string]any {
	t.Helper()
	status, raw := ep.GetJSON(t,
		"/v1/instances/"+instanceID+"/messages?sender_kind=publisher", "")
	if status != http.StatusOK {
		t.Fatalf("GET /instances/%s/messages: %d %s", instanceID, status, string(raw))
	}
	var resp struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode messages response: %v: %s", err, string(raw))
	}
	matches := make([]map[string]any, 0, len(resp.Messages))
	for _, m := range resp.Messages {
		if m["sender_kind"] != "publisher" {
			continue
		}
		if m["sender"] != wantSender {
			t.Fatalf("publisher message persisted with sender=%v, want %q — rimsky must "+
				"derive sender from publisher_subscriptions.publisher_name, not the "+
				"sensor's request body sender", m["sender"], wantSender)
		}
		// @constraint: find the message that carries the real inbound
		// marker — the probe POSTs the wait emitted will also be present
		// under the same sender; the proof message is the one carrying the
		// test's distinctive id marker.
		if payloadCarriesMarker(m, "ck-sensor-webhook-payload-marker") {
			matches = append(matches, m)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly 1 persisted publisher message carrying the inbound "+
			"marker; got %d. Total publisher messages persisted = %d",
			len(matches), len(resp.Messages))
	}
	return matches[0]
}

// payloadCarriesMarker checks the message's payload.body.id matches
// the distinctive marker the inbound POST set. The sensor's
// serveWebhook decodes the JSON body and lands it under
// envelope.payload.body (raw JSON bytes wrapped under "body" when the
// inbound JSON-decoded cleanly). This is the projection the test reads
// back to refute the canned-payload prong.
func payloadCarriesMarker(message map[string]any, marker string) bool {
	payload, ok := message["payload"].(map[string]any)
	if !ok {
		return false
	}
	body, ok := payload["body"].(map[string]any)
	if !ok {
		return false
	}
	id, _ := body["id"].(string)
	return id == marker
}

// requirePersistedWebhookPayload asserts the persisted message's
// payload reflects the EXACT inbound bytes — same nested object shape,
// same scalar values, same array elements, plus the sensor's URL-path
// stamp. A canned-payload defect would surface as one of the equality
// checks failing.
func requirePersistedWebhookPayload(t *testing.T, persisted map[string]any, inbound map[string]any, expectedPath string) {
	t.Helper()
	payload, ok := persisted["payload"].(map[string]any)
	if !ok {
		t.Fatalf("persisted message has no payload object: %+v", persisted)
	}
	gotPath, _ := payload["path"].(string)
	if gotPath != expectedPath {
		t.Fatalf("persisted message payload.path = %q, want %q — the sensor must stamp "+
			"the inbound URL path through to the message; a canned payload would not",
			gotPath, expectedPath)
	}
	gotMethod, _ := payload["method"].(string)
	if gotMethod != http.MethodPost {
		t.Fatalf("persisted message payload.method = %q, want %q",
			gotMethod, http.MethodPost)
	}
	body, ok := payload["body"].(map[string]any)
	if !ok {
		t.Fatalf("persisted message payload.body is not an object — the sensor did not "+
			"JSON-decode the inbound body; canned-string fallback was used: %+v",
			payload["body"])
	}
	// @constraint: element-wise equality on every scalar / array / nested
	// object the inbound carried. JSON round-trips numbers as float64, so
	// a deep equality on the inbound vs the persisted body must round-trip
	// inbound through json.Marshal/Unmarshal first.
	wantBody := jsonRoundTrip(t, inbound)
	if !deepEqualJSON(wantBody, body) {
		gotBytes, _ := json.Marshal(body)
		wantBytes, _ := json.Marshal(wantBody)
		t.Fatalf("persisted message payload.body did not reflect the inbound bytes:\n"+
			"  got  = %s\n  want = %s\n"+
			"— the sensor's request-body translation is canned or lossy",
			string(gotBytes), string(wantBytes))
	}
}

// jsonRoundTrip marshals + unmarshals the value through encoding/json
// so the comparison shape (numbers as float64, etc.) matches the
// persisted message's deserialized form.
func jsonRoundTrip(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal for round-trip: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal for round-trip: %v", err)
	}
	return out
}

// deepEqualJSON compares two JSON-decoded values for structural
// equality. It handles map[string]any / []any / scalars without relying
// on reflect.DeepEqual, whose behavior on []any with different
// allocation backing is order-sensitive in a way that matches our
// intent (array order MUST be preserved on the wire).
func deepEqualJSON(a, b any) bool {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, va := range av {
			vb, ok := bv[k]
			if !ok {
				return false
			}
			if !deepEqualJSON(va, vb) {
				return false
			}
		}
		return true
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !deepEqualJSON(av[i], bv[i]) {
				return false
			}
		}
		return true
	default:
		return a == b
	}
}

// deploySensorWebhookTemplate POSTs the sensor-webhook template and
// deploys it. The template wires:
//
//   - a publisher `intake` (kind webhook) with `path_prefix=/wh/ingest`.
//     The path_prefix is the LOAD-BEARING under-test piece for the
//     falsifier's "path-prefix filter declared but unused" prong.
//   - a `reactor` node subscribing to the invalidate envelope topic so
//     the cascade has a real target. The reactor uses the `stub`
//     executor declared on the harness side; the proof axis here is
//     the PERSISTED publisher message in rimsky, not the reactor's
//     run.
func deploySensorWebhookTemplate(t *testing.T, ep harness.RimskyEndpoint) string {
	t.Helper()
	sensorConfig := map[string]any{
		"path_prefix": webhookPathPrefix,
	}
	configBytes, err := json.Marshal(sensorConfig)
	if err != nil {
		t.Fatalf("marshal sensor config: %v", err)
	}
	body := map[string]any{
		"spec": map[string]any{
			"name":             "sensor-webhook-e2e",
			"version":          "1",
			"frame_timeout_ms": 600000,
			"messages": []map[string]any{
				{
					// @constraint: the webhook sensor emits payload with
					// {observed_at, path, method, body, [idempotency_key]};
					// the schema is loose (additionalProperties allowed)
					// because the `body:` subfield is whatever the inbound
					// POST sent and the test asserts equality on real
					// bytes, not a strict shape.
					"type": webhookMessageType,
					"body_schema": map[string]any{
						"type":                 "object",
						"properties":           map[string]any{},
						"additionalProperties": true,
					},
				},
			},
			"nodes": []map[string]any{
				{
					"type":     webhookReactorNode,
					"executor": "stub",
					"subscribes": []map[string]any{
						{
							"node":                   webhookMessageType,
							"type":                   "terminal/success",
							"wake_on_change":         true,
							"force_upstream_refresh": false,
						},
					},
				},
			},
			"publishers": []map[string]any{
				{
					"name":         webhookPublisherName,
					"kind":         "webhook",
					"config":       json.RawMessage(configBytes),
					"target_node":  webhookReactorNode,
					"message_type": webhookMessageType,
				},
			},
		},
	}
	status, raw := ep.PostJSON(t, "/v1/templates", body)
	if status != http.StatusCreated {
		t.Fatalf("POST /templates: %d %s", status, string(raw))
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
	deployStatus, deployRaw := ep.PostJSON(t,
		"/v1/templates/"+resp.TemplateID+"/deploy", map[string]any{})
	if deployStatus != http.StatusOK {
		t.Fatalf("POST /templates/%s/deploy: %d %s",
			resp.TemplateID, deployStatus, string(deployRaw))
	}
	return resp.TemplateID
}

// createSensorWebhookInstance POSTs a new instance for the template
// and returns its id. The POST triggers rimsky's
// StartPublisherSubscriptionsForInstance which generates the
// publisher_subscription_id and calls Subscribe on the sensor peer —
// which is where the path mounts in the sensor's in-memory router.
func createSensorWebhookInstance(t *testing.T, ep harness.RimskyEndpoint, templateID, instanceKey string) string {
	t.Helper()
	status, raw := ep.PostJSON(t, "/v1/instances", map[string]any{
		"template":     templateID,
		"instance_key": instanceKey,
		"params":       map[string]any{},
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /instances: %d %s", status, string(raw))
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
