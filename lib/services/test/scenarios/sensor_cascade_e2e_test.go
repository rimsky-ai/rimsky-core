// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
)

const reactorNodeAlias = "reactor"

const quiescentSensorPolls = 6

const reactorMessageType = "invalidate/reactor"
const bystanderMessageType = "refresh/reactor"

func TestSensorHTTP_RealExternalChangeFiresDownstreamNode(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	var (
		bodyMu   sync.RWMutex
		body     = `{"state":"initial"}`
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
	rimskyAlias := harness.NextRimskyAlias()
	rimskyInternalURL := fmt.Sprintf("http://%s:8080", rimskyAlias)
	sensorEP := harness.StartSensorHTTP(ctx, t, netName, "sensor-http", rimskyInternalURL, hostPort)

	execEP := harness.StartExecutorStubOnNetwork(ctx, t, netName)

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithRimskyAlias(rimskyAlias),
		harness.WithExecutor("stub", execEP),
		harness.WithPublisher("watcher", sensorEP),
	)

	watchedURL := fmt.Sprintf("http://host.testcontainers.internal:%d/", hostPort)

	templateID := deploySensorCascadeTemplate(t, ep, watchedURL)
	instanceID := createSensorCascadeInstance(t, ep, templateID, "ck-sensor-cascade")

	ep.WaitForSubscriptionsActive(t, instanceID)

	ep.WaitForNodeSettledTo(t, instanceID, reactorNodeAlias, "fresh")

	waitForDispatchQuiescent(t, ep, instanceID, reactorNodeAlias, &pollHits)
	waitForDispatchQuiescent(t, ep, instanceID, "bystander", &pollHits)
	bystanderBaseline := workStartedCount(t, ep, instanceID, "bystander")
	reactorBaseline := workStartedCount(t, ep, instanceID, reactorNodeAlias)

	// @story: sensor-http
	bodyMu.Lock()
	body = `{"state":"changed"}`
	bodyMu.Unlock()

	// @story: cascade-signal-blind
	requireReactorReran(t, ep, instanceID, reactorNodeAlias, reactorBaseline)

	// @story: publisher-protocol
	requirePublisherMessagePersisted(t, ep, instanceID, "watcher")

	requireBystanderDidNotReRun(t, ep, instanceID, "bystander", bystanderBaseline, &pollHits)
}

// @story: sensor-http
func TestSensorHTTP_DurableAcrossFires(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	var (
		bodyMu   sync.RWMutex
		body     = `{"state":"initial"}`
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
	rimskyAlias := harness.NextRimskyAlias()
	rimskyInternalURL := fmt.Sprintf("http://%s:8080", rimskyAlias)
	sensorEP := harness.StartSensorHTTP(ctx, t, netName, "sensor-http", rimskyInternalURL, hostPort)
	execEP := harness.StartExecutorStubOnNetwork(ctx, t, netName)

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithRimskyAlias(rimskyAlias),
		harness.WithExecutor("stub", execEP),
		harness.WithPublisher("watcher", sensorEP),
	)

	watchedURL := fmt.Sprintf("http://host.testcontainers.internal:%d/", hostPort)

	templateID := deploySensorCascadeTemplate(t, ep, watchedURL)
	instanceID := createSensorCascadeInstance(t, ep, templateID, "ck-sensor-durable")

	ep.WaitForSubscriptionsActive(t, instanceID)

	ep.WaitForNodeSettledTo(t, instanceID, reactorNodeAlias, "fresh")
	waitForDispatchQuiescent(t, ep, instanceID, reactorNodeAlias, &pollHits)
	waitForDispatchQuiescent(t, ep, instanceID, "bystander", &pollHits)
	bystanderBaseline := workStartedCount(t, ep, instanceID, "bystander")

	requireInstanceNotTerminated(t, ep, instanceID)

	// @story: sensor-http
	const fires = 3
	for i := 1; i <= fires; i++ {
		reactorBefore := workStartedCount(t, ep, instanceID, reactorNodeAlias)

		bodyMu.Lock()
		body = fmt.Sprintf(`{"state":"changed-%d"}`, i)
		bodyMu.Unlock()

		// @story: cascade-signal-blind
		requireWorkStartedGrew(t, ep, instanceID, reactorNodeAlias, reactorBefore,
			fmt.Sprintf("fire %d/%d", i, fires))
		ep.WaitForNodeSettledTo(t, instanceID, reactorNodeAlias, "fresh")
		requireInstanceNotTerminated(t, ep, instanceID)
	}

	requireBystanderDidNotReRun(t, ep, instanceID, "bystander", bystanderBaseline, &pollHits)
}

func requireInstanceNotTerminated(t *testing.T, ep harness.RimskyEndpoint, instanceID string) {
	t.Helper()
	status, raw := ep.GetJSON(t, "/v1/instances/"+instanceID, "")
	if status != http.StatusOK {
		t.Fatalf("GET /instances/%s: %d %s", instanceID, status, string(raw))
	}
	var resp struct {
		TerminatedAt *string `json:"terminated_at"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode instance %s: %v: %s", instanceID, err, string(raw))
	}
	if resp.TerminatedAt != nil && *resp.TerminatedAt != "" {
		t.Fatalf("durable instance %s was reaped (terminated_at=%q) — a no-flag "+
			"instance must stay alive across fires; auto-terminate-on-drain must be gone",
			instanceID, *resp.TerminatedAt)
	}
}

func requireWorkStartedGrew(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string, baseline int, label string) {
	t.Helper()
	awaited.Until(t, fmt.Sprintf("reactor node %q to re-run on %s, growing work_started past %d — a durable "+
		"instance that stopped reacting, or was reaped, never gets here", nodeType, label, baseline), func() bool {
		return workStartedCount(t, ep, instanceID, nodeType) > baseline
	})
}

func deploySensorCascadeTemplate(t *testing.T, ep harness.RimskyEndpoint, watchedURL string) string {
	t.Helper()

	sensorConfig := map[string]any{
		"url":           watchedURL,
		"poll_interval": "1s",
		"match": map[string]any{
			"status": []int{200},
		},
	}
	configBytes, err := json.Marshal(sensorConfig)
	if err != nil {
		t.Fatalf("marshal sensor config: %v", err)
	}

	body := map[string]any{
		"spec": map[string]any{
			"name":    "sensor-cascade",
			"version": "1",
			"messages": []map[string]any{
				{
					"type": reactorMessageType,
					"body_schema": map[string]any{
						"type":                 "object",
						"properties":           map[string]any{},
						"additionalProperties": true,
					},
				},
				{
					"type": bystanderMessageType,
					"body_schema": map[string]any{
						"type":                 "object",
						"properties":           map[string]any{},
						"additionalProperties": true,
					},
				},
			},
			"nodes": []map[string]any{
				{
					"type":     reactorNodeAlias,
					"executor": "stub",
					"subscribes": []map[string]any{
						{
							"node":                   reactorMessageType,
							"type":                   "terminal/success",
							"force_upstream_refresh": false,
						},
					},
				},
				{
					"type":     "bystander",
					"executor": "stub",
					"subscribes": []map[string]any{
						{
							"node":                   bystanderMessageType,
							"type":                   "terminal/success",
							"force_upstream_refresh": false,
						},
					},
				},
			},
			"publishers": []map[string]any{
				{
					"name":         "watcher",
					"kind":         "http",
					"config":       json.RawMessage(configBytes),
					"message_type": reactorMessageType,
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
	deployStatus, deployRaw := ep.PostJSON(t, "/v1/templates/"+resp.TemplateID+"/deploy", map[string]any{})
	if deployStatus != http.StatusOK {
		t.Fatalf("POST /templates/%s/deploy: %d %s", resp.TemplateID, deployStatus, string(deployRaw))
	}
	return resp.TemplateID
}

func createSensorCascadeInstance(t *testing.T, ep harness.RimskyEndpoint, templateID, instanceKey string) string {
	t.Helper()
	status, raw := ep.PostJSON(t, "/v1/instances", map[string]any{
		"template":      templateID,
		"instance_key":  instanceKey,
		"params":        map[string]any{},
		"target_daemon": "scenario-default-daemon",
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

func requireReactorReran(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string, baseline int) {
	t.Helper()
	awaited.Until(t, fmt.Sprintf("the sensor→emit→message-delivery→cascade loop to re-run reactor node %q, "+
		"growing work_started past %d", nodeType, baseline), func() bool {
		return workStartedCount(t, ep, instanceID, nodeType) > baseline
	})
	ep.WaitForNodeSettledTo(t, instanceID, nodeType, "fresh")
}

func workStartedCount(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string) int {
	t.Helper()
	status, obs, raw := ep.GetNodeObservability(t, instanceID, nodeType)
	if status != http.StatusOK {
		t.Fatalf("harness: GET node observability %s/%s: status %d, want 200 — a broken observability "+
			"endpoint must fail loudly, not read as a silent 0 that would let a did-not-re-run "+
			"assertion pass vacuously: %s", instanceID, nodeType, status, string(raw))
	}
	n := 0
	for _, e := range obs.Events {
		if e.Kind == "work_started" {
			n++
		}
	}
	return n
}

func requirePublisherMessagePersisted(t *testing.T, ep harness.RimskyEndpoint, instanceID, wantSender string) {
	t.Helper()
	awaited.Until(t, fmt.Sprintf("the real sensor to emit a message with sender_kind=publisher into instance %s",
		instanceID), func() bool {
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
					"(rimsky must derive sender from the publisher-subscription's "+
					"publisher_name, not the request body's sender)", m.Sender, wantSender)
			}
			return true
		}
		return false
	})
}

func waitForDispatchQuiescent(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string, polls *atomic.Int32) {
	t.Helper()
	last := workStartedCount(t, ep, instanceID, nodeType)
	target := int(polls.Load()) + quiescentSensorPolls
	awaited.Until(t, fmt.Sprintf("node %q to hold its work_started count across %d consecutive sensor polls; "+
		"a perpetually-rerunning node never gets there", nodeType, quiescentSensorPolls), func() bool {
		cur := workStartedCount(t, ep, instanceID, nodeType)
		if cur != last {
			last = cur
			target = int(polls.Load()) + quiescentSensorPolls
			return false
		}
		return int(polls.Load()) >= target
	})
}

func requireBystanderDidNotReRun(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string, baseline int, polls *atomic.Int32) {
	t.Helper()
	target := int(polls.Load()) + quiescentSensorPolls
	awaited.Until(t, fmt.Sprintf("the sensor to complete %d more polls while bystander node %q holds at "+
		"work_started=%d", quiescentSensorPolls, nodeType, baseline), func() bool {
		if cur := workStartedCount(t, ep, instanceID, nodeType); cur > baseline {
			t.Fatalf("bystander node %q re-ran on the sensor fire (work_started grew %d → %d) — "+
				"its subscription matches `message/refresh/...`, NOT the `invalidate` envelope "+
				"the sensor emits, so the cascade must not have touched it; a spurious cascade "+
				"is a bug", nodeType, baseline, cur)
		}
		return int(polls.Load()) >= target
	})
}
