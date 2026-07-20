// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

const httpPublisherName = "watcher"

const httpReactorNode = "reactor"

const httpMessageType = "refresh/reactor"

const httpPollIntervalConfig = "1s"
const httpPollInterval = 1 * time.Second

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

	netName := harness.NewNetwork(ctx, t)
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

	ep.WaitForSubscriptionsActive(t, instanceID, 90*time.Second)

	// @story: sensor-http
	waitForUpstreamPolls(t, &pollHits, 3, 30*time.Second)
	requirePublisherMessageCount(t, ep, instanceID, 0, "body-filter-not-matching")

	// @story: sensor-http
	bodyMu.Lock()
	body = `{"deployment":{"status":"healthy","gen":1}}`
	bodyMu.Unlock()

	requirePublisherMessagePersistedHTTP(t, ep, instanceID, httpPublisherName,
		20*time.Second, "first-match-after-filter-flip")
	requirePublisherMessageCountStable(t, ep, instanceID, 1,
		5*httpPollInterval, "exactly-one-after-first-match")

	statePool := connectSensorStatePostgres(ctx, t, statePG.hostDSN)
	defer statePool.Close()
	subID, originalLastHash := waitForSensorHttpRowWithHash(t, ctx, statePool, 20*time.Second)
	t.Logf("sensor-http persisted subscription %s with last_hash=%s before restart",
		subID, originalLastHash)

	// @story: sensor-http
	preRestartCount := publisherMessageCount(t, ep, instanceID)
	preRestartPolls := pollHits.Load()
	sensor.Stop(ctx)
	t.Logf("sensor-http stopped; pre-restart message count=%d, upstream polls=%d",
		preRestartCount, preRestartPolls)

	time.Sleep(2 * httpPollInterval)
	if got := pollHits.Load(); got > preRestartPolls {
		t.Fatalf("upstream was polled (%d → %d) while sensor was stopped — the polling "+
			"is not happening from inside the sensor process", preRestartPolls, got)
	}

	sensor.Restart(ctx)
	postRestartAt := time.Now().UTC()
	t.Logf("sensor-http restarted at %s; recovered watermark should suppress re-emit",
		postRestartAt.Format(time.RFC3339Nano))

	waitForUpstreamPolls(t, &pollHits, int(preRestartPolls)+3, 30*time.Second)
	requirePublisherMessageCountStable(t, ep, instanceID, preRestartCount,
		5*httpPollInterval, "watermark-suppressed-re-emit-after-restart")

	// @story: sensor-http
	bodyMu.Lock()
	body = `{"deployment":{"status":"healthy","gen":2}}`
	bodyMu.Unlock()

	requirePublisherMessageCountReaches(t, ep, instanceID, preRestartCount+1,
		20*time.Second, "second-match-after-restart")
	requirePublisherMessageCountStable(t, ep, instanceID, preRestartCount+1,
		5*httpPollInterval, "exactly-one-after-second-match")

	requireSensorHttpHashAdvanced(t, ctx, statePool, subID, originalLastHash, 20*time.Second)
}

func waitForSensorHttpRowWithHash(t *testing.T, ctx context.Context, pool *pgxpool.Pool, deadline time.Duration) (string, string) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		var subID, lastHash string
		err := pool.QueryRow(ctx,
			`SELECT publisher_subscription_id, COALESCE(last_hash, '')
			   FROM sensor_http_state
			  LIMIT 1`,
		).Scan(&subID, &lastHash)
		if err == nil && lastHash != "" {
			return subID, lastHash
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("sensor-http never persisted a non-empty last_hash within %v — "+
		"the emit path is not writing the body-hash watermark through to the durable "+
		"state DB; the restart-recovery proof is unobservable without it", deadline)
	return "", ""
}

func requireSensorHttpHashAdvanced(t *testing.T, ctx context.Context, pool *pgxpool.Pool, subID, original string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	var last string
	for time.Now().Before(end) {
		var hash string
		err := pool.QueryRow(ctx,
			`SELECT COALESCE(last_hash, '') FROM sensor_http_state WHERE publisher_subscription_id = $1`,
			subID,
		).Scan(&hash)
		if err == nil {
			last = hash
			if hash != "" && hash != original {
				return
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("sensor-http did not advance last_hash past %q within %v (last seen %q) — "+
		"the durable watermark must move forward on each successful emit, otherwise the "+
		"next poll on the same body would re-emit silently",
		original, deadline, last)
}

func waitForUpstreamPolls(t *testing.T, counter *atomic.Int32, min int, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if int(counter.Load()) >= min {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("upstream was polled only %d times in %v (wanted >=%d) — the sensor "+
		"is not honoring its declared poll_interval=%s", counter.Load(), deadline, min, httpPollIntervalConfig)
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

func requirePublisherMessageCountReaches(t *testing.T, ep harness.RimskyEndpoint, instanceID string, want int, deadline time.Duration, label string) {
	t.Helper()
	end := time.Now().Add(deadline)
	var last int
	for time.Now().Before(end) {
		last = publisherMessageCount(t, ep, instanceID)
		if last >= want {
			if last > want {
				t.Fatalf("publisher message count for %s overshot: got %d, want %d (%s)",
					instanceID, last, want, label)
			}
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("publisher message count for %s never reached %d within %v (last=%d, %s) — "+
		"the sensor's poll-and-emit path did not fire on the expected body change",
		instanceID, want, deadline, last, label)
}

func requirePublisherMessageCountStable(t *testing.T, ep harness.RimskyEndpoint, instanceID string, want int, window time.Duration, label string) {
	t.Helper()
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		got := publisherMessageCount(t, ep, instanceID)
		if got != want {
			t.Fatalf("publisher message count for %s drifted off baseline during stability "+
				"window: got %d, want %d (%s) — a sensor that ignored the body filter or "+
				"lost its watermark on restart would surface here",
				instanceID, got, want, label)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func requirePublisherMessagePersistedHTTP(t *testing.T, ep harness.RimskyEndpoint, instanceID, wantSender string, deadline time.Duration, label string) {
	t.Helper()
	end := time.Now().Add(deadline)
	var lastSeen string
	for time.Now().Before(end) {
		status, raw := ep.GetJSON(t,
			"/v1/instances/"+instanceID+"/messages?sender_kind=publisher", "")
		if status == http.StatusOK {
			var resp struct {
				Messages []struct {
					Type       string `json:"type"`
					Sender     string `json:"sender"`
					SenderKind string `json:"sender_kind"`
				} `json:"messages"`
			}
			if err := json.Unmarshal(raw, &resp); err == nil {
				for _, m := range resp.Messages {
					lastSeen = fmt.Sprintf("type=%s sender=%s sender_kind=%s",
						m.Type, m.Sender, m.SenderKind)
					if m.SenderKind != "publisher" {
						continue
					}
					if m.Sender != wantSender {
						t.Fatalf("publisher message persisted with sender=%q, want %q "+
							"(%s) — rimsky must derive sender from "+
							"publisher_subscriptions.publisher_name, not the sensor's "+
							"request body sender", m.Sender, wantSender, label)
					}
					return
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("no publisher message landed for instance %s within %v (%s); last seen=%q — "+
		"the real sensor never emitted into rimsky after the matching body change",
		instanceID, deadline, label, lastSeen)
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
