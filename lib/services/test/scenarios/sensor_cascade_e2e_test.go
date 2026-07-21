// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

const reactorNodeAlias = "reactor"

const reactorMessageType = "invalidate/reactor"
const bystanderMessageType = "refresh/reactor"

func TestSensorHTTP_RealExternalChangeFiresDownstreamNode(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	var (
		bodyMu sync.RWMutex
		body   = `{"state":"initial"}`
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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

	ep.WaitForSubscriptionsActive(t, instanceID, 90*time.Second)

	ep.WaitForNodeSettledTo(t, instanceID, reactorNodeAlias, "fresh", 90*time.Second)
	ep.WaitForNodeSettledTo(t, instanceID, "bystander", "fresh", 90*time.Second)

	waitForDispatchQuiescent(t, ep, instanceID, reactorNodeAlias, 60*time.Second)
	waitForDispatchQuiescent(t, ep, instanceID, "bystander", 60*time.Second)
	bystanderBaseline := workStartedCount(t, ep, instanceID, "bystander")
	reactorBaseline := workStartedCount(t, ep, instanceID, reactorNodeAlias)

	// @story: sensor-http
	bodyMu.Lock()
	body = `{"state":"changed"}`
	bodyMu.Unlock()

	// @story: cascade-signal-blind
	requireReactorReran(t, ep, instanceID, reactorNodeAlias, reactorBaseline, 120*time.Second)

	// @story: publisher-protocol
	requirePublisherMessagePersisted(t, ep, instanceID, "watcher")

	requireBystanderDidNotReRun(t, ep, instanceID, "bystander", bystanderBaseline)
}

// @story: sensor-http
func TestSensorHTTP_DurableAcrossFires(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	var (
		bodyMu sync.RWMutex
		body   = `{"state":"initial"}`
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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

	ep.WaitForSubscriptionsActive(t, instanceID, 90*time.Second)

	ep.WaitForNodeSettledTo(t, instanceID, reactorNodeAlias, "fresh", 90*time.Second)
	ep.WaitForNodeSettledTo(t, instanceID, "bystander", "fresh", 90*time.Second)
	waitForDispatchQuiescent(t, ep, instanceID, reactorNodeAlias, 60*time.Second)
	waitForDispatchQuiescent(t, ep, instanceID, "bystander", 60*time.Second)
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
		requireWorkStartedGrew(t, ep, instanceID, reactorNodeAlias, reactorBefore, 120*time.Second,
			fmt.Sprintf("fire %d/%d", i, fires))
		ep.WaitForNodeSettledTo(t, instanceID, reactorNodeAlias, "fresh", 30*time.Second)
		requireInstanceNotTerminated(t, ep, instanceID)
	}

	requireBystanderDidNotReRun(t, ep, instanceID, "bystander", bystanderBaseline)
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

func requireWorkStartedGrew(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string, baseline int, deadline time.Duration, label string) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if workStartedCount(t, ep, instanceID, nodeType) > baseline {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("reactor node %q did not re-run on %s (work_started stayed at %d) within %v — "+
		"the durable instance stopped reacting; a reaped instance would explain this",
		nodeType, label, baseline, deadline)
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

func requireReactorReran(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string, baseline int, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if workStartedCount(t, ep, instanceID, nodeType) > baseline {
			ep.WaitForNodeSettledTo(t, instanceID, nodeType, "fresh", 30*time.Second)
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("reactor node %q did not re-run after the real external change within %v "+
		"(work_started count stayed at %d) — the sensor→emit→message-delivery→cascade "+
		"loop did not fire the downstream node", nodeType, deadline, baseline)
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
	end := time.Now().Add(60 * time.Second)
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
					lastSeen = fmt.Sprintf("type=%s sender=%s sender_kind=%s", m.Type, m.Sender, m.SenderKind)
					if m.SenderKind != "publisher" {
						continue
					}
					if m.Sender != wantSender {
						t.Fatalf("publisher message persisted with sender=%q, want %q "+
							"(rimsky must derive sender from the publisher-subscription's "+
							"publisher_name, not the request body's sender)", m.Sender, wantSender)
					}
					return
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("no message with sender_kind=publisher persisted for instance %s within deadline; "+
		"last seen=%q — the real sensor never emitted into rimsky", instanceID, lastSeen)
}

func waitForDispatchQuiescent(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string, deadline time.Duration) {
	t.Helper()
	const stableWindow = 6 * time.Second
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
	t.Fatalf("node %q never went dispatch-quiescent within %v (work_started kept growing, last=%d) — "+
		"a perpetually-rerunning node is a defect", nodeType, deadline, last)
}

func requireBystanderDidNotReRun(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string, baseline int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cur := workStartedCount(t, ep, instanceID, nodeType); cur > baseline {
			t.Fatalf("bystander node %q re-ran on the sensor fire (work_started grew %d → %d) — "+
				"its subscription matches `message/refresh/...`, NOT the `invalidate` envelope "+
				"the sensor emits, so the cascade must not have touched it; a spurious cascade "+
				"is a bug", nodeType, baseline, cur)
		}
		time.Sleep(250 * time.Millisecond)
	}
}
