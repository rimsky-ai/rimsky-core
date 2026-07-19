// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: sensor
// @concept: publisher
// @concept: publisher-subscription
// @story: sensor-webhook
package scenarios

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

const webhookPublisherName = "intake"

const webhookReactorNode = "reactor"

const webhookMessageType = "invalidate/reactor"

const webhookPathPrefix = "/wh/ingest"

const webhookAuthSecret = "e2e-shared-hmac-secret"

const webhookSignatureHeader = "X-Rimsky-Signature"

const webhookTimestampHeader = "X-Rimsky-Timestamp"

func TestSensorWebhook_InboundPostPersistsBeforeAck(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.NewNetwork(ctx, t)

	rimskyAlias := harness.NextRimskyAlias()
	rimskyInternalURL := fmt.Sprintf("http://%s:8080", rimskyAlias)
	sensor := harness.StartSensorWebhook(ctx, t, netName, "sensor-webhook", rimskyInternalURL)

	execEP := harness.StartExecutorStubOnNetwork(ctx, t, netName)

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithRimskyAlias(rimskyAlias),
		harness.WithExecutor("stub", execEP),
		harness.WithPublisher(webhookPublisherName, sensor.GRPCEndpoint),
	)

	templateID := deploySensorWebhookTemplate(t, ep)
	instanceID := createSensorWebhookInstance(t, ep, templateID, "ck-sensor-webhook-e2e")

	ep.WaitForSubscriptionsActive(t, instanceID, 90*time.Second)

	waitForWebhookSubscriptionActive(t, sensor.WebhookBaseURL, webhookPathPrefix, 30*time.Second)

	preCount := publisherMessageCount(t, ep, instanceID)
	outsideStatus, outsideBody := postWebhook(t, sensor.WebhookBaseURL+"/wh/unrelated",
		[]byte(`{"should":"not-route"}`))
	if outsideStatus != http.StatusNotFound {
		t.Fatalf("POST to path OUTSIDE prefix returned %d (want 404), body=%q — the "+
			"path-prefix filter is declared but unused; any POST is being routed",
			outsideStatus, string(outsideBody))
	}
	requirePublisherMessageCountStable(t, ep, instanceID, preCount, 2*time.Second,
		"off-prefix-post-must-not-emit")

	postPath := sensor.WebhookBaseURL + webhookPathPrefix

	beforeAnon := publisherMessageCount(t, ep, instanceID)
	anonStatus, anonBody := postWebhook(t, postPath, []byte(`{"forged":"no-signature"}`))
	if anonStatus != http.StatusUnauthorized {
		t.Fatalf("bare unsigned POST to %s returned %d (want 401) body=%q — the webhook "+
			"sensor must REJECT callers that cannot produce a valid HMAC signature; an "+
			"accepted anonymous POST is the finding-1947 injection vulnerability",
			postPath, anonStatus, string(anonBody))
	}
	requirePublisherMessageCountStable(t, ep, instanceID, beforeAnon, 2*time.Second,
		"unsigned-post-must-not-emit")

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

	beforePost := publisherMessageCount(t, ep, instanceID)

	status, ackBody := postWebhookSigned(t, postPath, rawBody, webhookAuthSecret)
	if status != http.StatusOK {
		t.Fatalf("correctly-signed POST to %s returned %d (want 200), body=%q — the sensor "+
			"did not accept the HMAC-signed POST on the configured path; the path-prefix "+
			"mount or the signature verification is broken", postPath, status, string(ackBody))
	}

	immediateCount := publisherMessageCount(t, ep, instanceID)
	if immediateCount != beforePost+1 {
		t.Fatalf("persisted publisher message count was %d immediately after the inbound "+
			"200 ack (want %d) — the sensor acknowledged the inbound POST BEFORE rimsky "+
			"persisted the message; serveWebhook must block on postMessage",
			immediateCount, beforePost+1)
	}

	persisted := readSinglePublisherMessage(t, ep, instanceID, webhookPublisherName)
	requirePersistedWebhookPayload(t, persisted, inboundBody, webhookPathPrefix)
}

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

func webhookSignature(secret, ts string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func postWebhookSigned(t *testing.T, url string, body []byte, secret string) (int, []byte) {
	t.Helper()
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build signed POST %s: %v", url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(webhookSignatureHeader, webhookSignature(secret, ts, body))
	req.Header.Set(webhookTimestampHeader, ts)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("signed POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

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
			if resp.StatusCode == http.StatusUnauthorized {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("sensor-webhook subscription did not become active on %s within %v "+
		"(last status=%d, want 401 once the path is mounted and rejecting unsigned "+
		"callers) — Subscribe handshake from rimsky did not mount the path",
		pathPrefix, deadline, lastStatus)
}

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

func deploySensorWebhookTemplate(t *testing.T, ep harness.RimskyEndpoint) string {
	t.Helper()
	sensorConfig := map[string]any{
		"path_prefix": webhookPathPrefix,
		"auth": map[string]any{
			"mode":             "hmac",
			"secret":           webhookAuthSecret,
			"signature_header": webhookSignatureHeader,
			"timestamp_header": webhookTimestampHeader,
		},
	}
	configBytes, err := json.Marshal(sensorConfig)
	if err != nil {
		t.Fatalf("marshal sensor config: %v", err)
	}
	body := map[string]any{
		"spec": map[string]any{
			"name":    "sensor-webhook-e2e",
			"version": "1",
			"messages": []map[string]any{
				{
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
