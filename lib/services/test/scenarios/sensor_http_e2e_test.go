// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: sensor
// @concept: publisher
// @concept: publisher-subscription
// @story: sensor-http
package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
)

const httpPublisherName = "watcher"

const httpReactorNode = "reactor"

const httpMessageType = "refresh/reactor"

const httpPollIntervalConfig = "1s"

func TestSensorHttp_BodyFilterAndDurableWatermark(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	var (
		bodyMu   sync.RWMutex
		body     = `{"deployment":{"status":"pending","gen":0}}`
		pollHits atomic.Int32
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		pollHits.Add(1)
		bodyMu.RLock()
		cur := body
		bodyMu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(cur))
	}))
	t.Cleanup(upstream.Close)

	hostPort := hostPortOf(t, upstream.URL)

	netName := harness.SharedNetworkName(ctx, t)
	statePG := startSensorStatePostgres(ctx, t, netName, "sensor-http-pg")

	rimskyAlias := harness.NextRimskyAlias()
	rimskyInternalURL := fmt.Sprintf("http://%s:8080", rimskyAlias)
	sensor := harness.StartSensorHTTPHandle(ctx, t, netName, "sensor-http", rimskyInternalURL, statePG.internalDSN, hostPort)

	execEP := harness.StartExecutorStubOnNetwork(ctx, t, netName)

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithRimskyAlias(rimskyAlias),
		harness.WithExecutor("stub", execEP),
		harness.WithPublisher(httpPublisherName, sensor.Endpoint),
	)

	watchedURL := fmt.Sprintf("http://host.testcontainers.internal:%d/", hostPort)

	templateID := deploySensorHttpTemplate(t, ep, watchedURL)
	instanceID := createSensorHttpInstance(t, ep, templateID, "ck-sensor-http-e2e")

	ep.WaitForSubscriptionsActive(t, instanceID)

	// @story: sensor-http
	waitForUpstreamPolls(t, &pollHits, 3)
	requirePublisherMessageCount(t, ep, instanceID, 0, "body-filter-not-matching")

	// @story: sensor-http
	bodyMu.Lock()
	body = `{"deployment":{"status":"healthy","gen":1}}`
	bodyMu.Unlock()

	requirePublisherMessagePersistedHTTP(t, ep, instanceID, httpPublisherName,
		"first-match-after-filter-flip")
	requirePublisherMessageCountStableWhile(t, ep, instanceID, 1,
		"exactly-one-after-first-match", upstreamPollsAdvanced(&pollHits, 5))

	statePool := connectSensorStatePostgres(ctx, t, statePG.hostDSN)
	defer statePool.Close()

	// @story: sensor-http
	preRestartCount := publisherMessageCount(t, ep, instanceID)
	sensor.Stop(ctx)
	preRestartPolls := pollHits.Load()

	subID, originalLastHash := waitForSensorHttpRowWithHash(t, ctx, statePool)
	t.Logf("sensor-http stopped; it left subscription %s at last_hash=%s, message count=%d, upstream polls=%d",
		subID, originalLastHash, preRestartCount, preRestartPolls)

	sensor.Restart(ctx)
	t.Log("sensor-http restarted; the recovered watermark should suppress a re-emit")

	waitForUpstreamPolls(t, &pollHits, int(preRestartPolls)+3)
	requirePublisherMessageCountStableWhile(t, ep, instanceID, preRestartCount,
		"watermark-suppressed-re-emit-after-restart", upstreamPollsAdvanced(&pollHits, 5))

	// @story: sensor-http
	bodyMu.Lock()
	body = `{"deployment":{"status":"healthy","gen":2}}`
	bodyMu.Unlock()

	requirePublisherMessageCountReaches(t, ep, instanceID, preRestartCount+1,
		"second-match-after-restart")
	requirePublisherMessageCountStableWhile(t, ep, instanceID, preRestartCount+1,
		"exactly-one-after-second-match", upstreamPollsAdvanced(&pollHits, 5))

	requireSensorHttpHashAdvanced(t, ctx, statePool, subID, originalLastHash)
}

func waitForSensorHttpRowWithHash(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (string, string) {
	t.Helper()
	var subID, lastHash string
	awaited.Until(t, "sensor-http to persist a non-empty body-hash watermark in its state DB", func() bool {
		err := pool.QueryRow(ctx,
			`SELECT publisher_subscription_id, COALESCE(last_hash, '')
			   FROM sensor_http_state
			  LIMIT 1`,
		).Scan(&subID, &lastHash)
		return err == nil && lastHash != ""
	})
	return subID, lastHash
}

func requireSensorHttpHashAdvanced(t *testing.T, ctx context.Context, pool *pgxpool.Pool, subID, original string) {
	t.Helper()
	awaited.Until(t, fmt.Sprintf("sensor-http to advance last_hash past %q on subscription %s", original, subID), func() bool {
		var hash string
		err := pool.QueryRow(ctx,
			`SELECT COALESCE(last_hash, '') FROM sensor_http_state WHERE publisher_subscription_id = $1`,
			subID,
		).Scan(&hash)
		return err == nil && hash != "" && hash != original
	})
}

func waitForUpstreamPolls(t *testing.T, counter *atomic.Int32, min int) {
	t.Helper()
	awaited.Until(t, fmt.Sprintf("the sensor to poll the upstream at least %d time(s) at its declared poll_interval=%s",
		min, httpPollIntervalConfig), func() bool { return int(counter.Load()) >= min })
}

func publisherMessageCount(t *testing.T, ep harness.RimskyEndpoint, instanceID string) int {
	t.Helper()
	status, raw := ep.GetJSON(t,
		"/v1/instances/"+instanceID+"/messages?sender_kind=publisher", "")
	if status != http.StatusOK {
		t.Fatalf("GET /instances/%s/messages: %d %s", instanceID, status, string(raw))
	}
	var resp struct {
		Messages []struct {
			Type       string `json:"type"`
			Sender     string `json:"sender"`
			SenderKind string `json:"sender_kind"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode messages response: %v: %s", err, string(raw))
	}
	n := 0
	for _, m := range resp.Messages {
		if m.SenderKind == "publisher" {
			n++
		}
	}
	return n
}

func requirePublisherMessageCount(t *testing.T, ep harness.RimskyEndpoint, instanceID string, want int, label string) {
	t.Helper()
	got := publisherMessageCount(t, ep, instanceID)
	if got != want {
		t.Fatalf("publisher message count for %s = %d, want %d (%s)",
			instanceID, got, want, label)
	}
}

func requirePublisherMessageCountReaches(t *testing.T, ep harness.RimskyEndpoint, instanceID string, want int, label string) {
	t.Helper()
	awaited.Until(t, fmt.Sprintf("the sensor's poll-and-emit path to bring instance %s to %d publisher message(s) (%s)",
		instanceID, want, label), func() bool {
		got := publisherMessageCount(t, ep, instanceID)
		if got > want {
			t.Fatalf("publisher message count for %s overshot: got %d, want %d (%s)",
				instanceID, got, want, label)
		}
		return got == want
	})
}

type sensorProgress struct {
	description string
	reached     func() bool
}

func requirePublisherMessageCountStableWhile(t *testing.T, ep harness.RimskyEndpoint, instanceID string,
	want int, label string, progress sensorProgress) {
	t.Helper()
	awaited.Until(t, fmt.Sprintf("%s while instance %s holds at %d publisher message(s) (%s)",
		progress.description, instanceID, want, label), func() bool {
		got := publisherMessageCount(t, ep, instanceID)
		if got != want {
			t.Fatalf("publisher message count for %s drifted off baseline while the sensor kept polling: "+
				"got %d, want %d (%s) — a sensor that ignored the body filter or lost its watermark on "+
				"restart would surface here", instanceID, got, want, label)
		}
		return progress.reached()
	})
}

func upstreamPollsAdvanced(counter *atomic.Int32, polls int) sensorProgress {
	target := int(counter.Load()) + polls
	return sensorProgress{
		description: fmt.Sprintf("the sensor to complete %d more upstream poll(s)", polls),
		reached:     func() bool { return int(counter.Load()) >= target },
	}
}

func requirePublisherMessagePersistedHTTP(t *testing.T, ep harness.RimskyEndpoint, instanceID, wantSender, label string) {
	t.Helper()
	awaited.Until(t, fmt.Sprintf("the sensor to emit a publisher message into instance %s (%s)", instanceID, label), func() bool {
		status, raw := ep.GetJSON(t,
			"/v1/instances/"+instanceID+"/messages?sender_kind=publisher", "")
		if status != http.StatusOK {
			return false
		}
		var resp struct {
			Messages []struct {
				Type       string `json:"type"`
				Sender     string `json:"sender"`
				SenderKind string `json:"sender_kind"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return false
		}
		for _, m := range resp.Messages {
			if m.SenderKind != "publisher" {
				continue
			}
			if m.Sender != wantSender {
				t.Fatalf("publisher message persisted with sender=%q, want %q "+
					"(%s) — rimsky must derive sender from "+
					"publisher_subscriptions.publisher_name, not the sensor's "+
					"request body sender", m.Sender, wantSender, label)
			}
			return true
		}
		return false
	})
}

func deploySensorHttpTemplate(t *testing.T, ep harness.RimskyEndpoint, watchedURL string) string {
	t.Helper()
	sensorConfig := map[string]any{
		"url":           watchedURL,
		"poll_interval": httpPollIntervalConfig,
		"match": map[string]any{
			"status": []int{200},
			"jsonpath": map[string]any{
				"path":  "deployment.status",
				"value": "healthy",
			},
		},
	}
	configBytes, err := json.Marshal(sensorConfig)
	if err != nil {
		t.Fatalf("marshal sensor config: %v", err)
	}
	return deployScenarioTemplate(t, ep, map[string]any{
		"spec": map[string]any{
			"name":    "sensor-http-e2e",
			"version": "1",
			"messages": []map[string]any{
				{
					"type": httpMessageType,
					"body_schema": map[string]any{
						"type":                 "object",
						"properties":           map[string]any{},
						"additionalProperties": true,
					},
				},
			},
			"nodes": []map[string]any{
				{
					"type":     httpReactorNode,
					"executor": "stub",
					"subscribes": []map[string]any{
						{
							"node":                   httpMessageType,
							"type":                   "terminal/success",
							"force_upstream_refresh": false,
						},
					},
				},
			},
			"publishers": []map[string]any{
				{
					"name":         httpPublisherName,
					"kind":         "http",
					"config":       json.RawMessage(configBytes),
					"message_type": httpMessageType,
				},
			},
		},
	})
}

func createSensorHttpInstance(t *testing.T, ep harness.RimskyEndpoint, templateID, instanceKey string) string {
	t.Helper()
	return createScenarioInstanceNoWake(t, ep, templateID, instanceKey)
}
