// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Gate 1 + Gate 8 — a real rimsky-sensor-http image observes a real
// external change and a real downstream node fires through rimsky's
// cascade; the publisher's message persists with sender_kind=publisher
// and a sender derived from the publisher identity.
//
// This is the headline acceptance scenario of `concept:cascade`
// ("reactivity to external change is the same machinery") and
// `concept:sensor` / `concept:publisher`. Every prior sensor test is
// client-and-server-in-one: it calls Tick() in-process against a pinned
// clock and POSTs to a fake rimsky, never starting the sensor binary,
// never reaching a real cascade. This test puts a REAL sensor image, a
// REAL external HTTP source, and rimsky's REAL message-delivery cascade
// together:
//
//   - A host-side httptest.Server is the watched external source. The
//     real rimsky-sensor-http image (a peer on the docker network) polls
//     it per its publisher-subscription's resolved_config.url, reachable
//     from inside the sensor container via host.testcontainers.internal.
//   - A template declares a publisher `watcher` (kind http) targeting the
//     `reactor` node, plus a `reactor` node that subscribes to the
//     sensor's message topic (message/invalidate/publisher/reactor) and a
//     `bystander` node subscribed to a NON-matching topic (the negative
//     control — it must NOT go stale when the sensor fires, proving the
//     cascade fired because of the matching subscription, not spuriously;
//     the sensor is a prebuilt image and cannot be source-reverted
//     in-test, so the negative control stands in for the coupling proof).
//   - Mutating the host server's body is the real external change. The
//     sensor observes the new content-hash and POSTs an invalidate
//     message to rimsky's POST /instances/{id}/messages with
//     sender_kind=publisher.
//
// Gate 1: the reactor node transitions stale → re-runs → fresh through
// the cascade. Gate 8: the persisted message carries sender_kind=publisher
// and a sender derived from the publisher-subscription identity (the
// publisher_name, not the request body's `sender`).
package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

// reactorTargetNode is both the publisher's target_node and the node
// that subscribes to the sensor's message topic. The publisher's
// message envelope carries target=<this>; the reactor subscribes to
// `message/invalidate/publisher/<this>` so the delivered envelope's
// signal type matches it.
const reactorTargetNode = "reactor"

// TestSensorHTTP_RealExternalChangeFiresDownstreamNode drives a real
// sensor image observing a real external change into rimsky's real
// cascade.
func TestSensorHTTP_RealExternalChangeFiresDownstreamNode(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// 1. Host-side watched source. The body is mutable under a mutex so
	//    the test can fire the "real external change" by swapping it.
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

	// 2. Network first, then the sensor (rimsky eager-dials its declared
	//    publishers at startup, so the sensor must be reachable on the
	//    network BEFORE BringUpRimsky). The sensor needs host-port access
	//    so it can dial the host-side httptest.Server from inside its
	//    container.
	netName := harness.NewNetwork(ctx, t)
	sensorEP := harness.StartSensorHTTP(ctx, t, netName, "sensor-http", hostPort)

	// Also wire a stub executor so the reactor node actually runs (the
	// reactor re-runs through its executor on cascade; without a real
	// executor the node would never reach fresh again).
	execEP := harness.StartExecutorStubOnNetwork(ctx, t, netName, "exec-ok")

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("stub", execEP),
		harness.WithPublisher("watcher", sensorEP),
	)

	// 3. The watched URL the sensor polls is per-subscription
	//    (resolved_config.url), reachable from the sensor container via
	//    the host-gateway alias. poll_interval=1s keeps the test bounded.
	watchedURL := fmt.Sprintf("http://host.testcontainers.internal:%d/", hostPort)

	templateID := deploySensorCascadeTemplate(t, ep, watchedURL)
	instanceID := createSensorCascadeInstance(t, ep, templateID, "ck-sensor-cascade")

	// 4. Drive the reactor to an initial fresh — its initial frame runs
	//    the stub executor and settles. The cascade assertion below is
	//    only meaningful once the node has first settled.
	waitForSensorNodeState(t, ep, instanceID, reactorTargetNode, "fresh", 90*time.Second)
	waitForSensorNodeState(t, ep, instanceID, "bystander", "fresh", 90*time.Second)

	// Both nodes are roots in the same instance, so their initial-frame
	// processing (and the recalculate fan-out that couples co-resident
	// roots) can still be draining when the first frame settles. Wait for
	// BOTH nodes' dispatch counts to go quiescent (stable across a window)
	// before firing, so any later growth is unambiguously attributable to
	// the sensor's emit and not to leftover initial-frame activity. Then
	// snapshot the bystander's settled dispatch count as the negative-
	// control baseline.
	waitForDispatchQuiescent(t, ep, instanceID, reactorTargetNode, 60*time.Second)
	waitForDispatchQuiescent(t, ep, instanceID, "bystander", 60*time.Second)
	bystanderBaseline := workStartedCount(t, ep, instanceID, "bystander")

	// 5. Fire the REAL external change: swap the host server's body so the
	//    sensor's next poll sees a different content-hash and emits.
	bodyMu.Lock()
	body = `{"state":"changed"}`
	bodyMu.Unlock()

	// 6. Gate 1: the reactor transitions stale (cascade fired) then
	//    re-runs to fresh. Poll for the stale-then-fresh round trip — we
	//    require evidence of a SECOND run (a fresh→stale→fresh round trip),
	//    not merely the terminal fresh it was already in.
	requireReactorReran(t, ep, instanceID, reactorTargetNode, 120*time.Second)

	// 7. Gate 8: the persisted message carries sender_kind=publisher and a
	//    sender derived from the publisher identity (the publisher_name
	//    "watcher", NOT the request body's `sender` which the sensor sets
	//    to "sensor-http"). rimsky overwrites `sender` from the
	//    publisher-subscription row for trust.
	requirePublisherMessagePersisted(t, ep, instanceID, "watcher")

	// 8. Negative control: the bystander subscribes to a NON-matching
	//    message topic (message/refresh/..., not the invalidate the sensor
	//    emits), so the sensor's fire must NOT re-run it. (A real
	//    coupling-proof revert is impossible against a prebuilt image; the
	//    negative control proves the cascade fired because of the matching
	//    subscription, not spuriously.) Asserted against the quiescent
	//    baseline so the bystander's legitimate initial run is excluded.
	requireBystanderDidNotReRun(t, ep, instanceID, "bystander", bystanderBaseline)
}

// TestSensorHTTP_DurableAcrossFires is the durable-by-default headline
// acceptance gate (spec scenario 1). It wires the SAME real
// rimsky-sensor-http image / real host source / real cascade as
// TestSensorHTTP_RealExternalChangeFiresDownstreamNode, but the behavior
// under test is rimsky's OWN lifecycle: a sensor-driven instance created
// with NO terminate_after_run flag must stay alive across MANY real
// external changes, re-running its reactor on every fire and never being
// reaped (terminated_at stays unset), with no publisher-subscription
// coupling in the terminal predicate.
//
// Why this is the meaningful gate: under the pre-fix auto-terminate-on-
// drain behavior the instance would terminate after its FIRST frame
// drained, so the 2nd fire would land on a terminated instance and the
// reactor would never re-run. Proving N≥3 fires each re-run the reactor
// while terminated_at stays NULL is the durable-by-default property end to
// end against the real assembled product.
func TestSensorHTTP_DurableAcrossFires(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Host-side watched source — mutable body, same shape as the sibling
	// test. Each distinct body is a fresh content-hash the sensor observes.
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

	netName := harness.NewNetwork(ctx, t)
	sensorEP := harness.StartSensorHTTP(ctx, t, netName, "sensor-http", hostPort)
	execEP := harness.StartExecutorStubOnNetwork(ctx, t, netName, "exec-ok")

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("stub", execEP),
		harness.WithPublisher("watcher", sensorEP),
	)

	watchedURL := fmt.Sprintf("http://host.testcontainers.internal:%d/", hostPort)

	templateID := deploySensorCascadeTemplate(t, ep, watchedURL)
	// NO terminate_after_run flag — durable by default. createSensorCascadeInstance
	// POSTs without the flag, exactly the default-instance path.
	instanceID := createSensorCascadeInstance(t, ep, templateID, "ck-sensor-durable")

	// Drive both root nodes to an initial fresh, then quiesce so later
	// dispatch growth is attributable to the sensor fires alone.
	waitForSensorNodeState(t, ep, instanceID, reactorTargetNode, "fresh", 90*time.Second)
	waitForSensorNodeState(t, ep, instanceID, "bystander", "fresh", 90*time.Second)
	waitForDispatchQuiescent(t, ep, instanceID, reactorTargetNode, 60*time.Second)
	waitForDispatchQuiescent(t, ep, instanceID, "bystander", 60*time.Second)
	bystanderBaseline := workStartedCount(t, ep, instanceID, "bystander")

	// After the initial frame settled, the durable instance must already be
	// un-terminated — this is the first place the old auto-terminate-on-drain
	// would have stamped terminated_at.
	requireInstanceNotTerminated(t, ep, instanceID)

	// Fire the REAL external change N≥3 times. Each distinct body is a new
	// content-hash; the sensor's next poll observes it and emits an
	// invalidate message that re-runs the reactor through the cascade.
	const fires = 3
	for i := 1; i <= fires; i++ {
		reactorBefore := workStartedCount(t, ep, instanceID, reactorTargetNode)

		bodyMu.Lock()
		body = fmt.Sprintf(`{"state":"changed-%d"}`, i)
		bodyMu.Unlock()

		// The reactor must re-run (work_started grows) on THIS fire …
		requireWorkStartedGrew(t, ep, instanceID, reactorTargetNode, reactorBefore, 120*time.Second,
			fmt.Sprintf("fire %d/%d", i, fires))
		// … settle back to fresh …
		waitForSensorNodeState(t, ep, instanceID, reactorTargetNode, "fresh", 30*time.Second)
		// … and the instance must STILL be un-terminated (the durable
		// property after each drain — never reaped).
		requireInstanceNotTerminated(t, ep, instanceID)
	}

	// Negative control: the bystander subscribes to a non-matching topic,
	// so none of the N fires must have re-run it.
	requireBystanderDidNotReRun(t, ep, instanceID, "bystander", bystanderBaseline)
}

// requireInstanceNotTerminated asserts GET /instances/{id} shows
// terminated_at unset — the durable-by-default property. The instances
// projection omits terminated_at entirely when NULL
// (json:"terminated_at,omitempty"), so a present, non-empty value is a
// reap that must not have happened on a durable (no-flag) instance.
func requireInstanceNotTerminated(t *testing.T, ep harness.RimskyEndpoint, instanceID string) {
	t.Helper()
	status, raw := ep.GetJSON(t, "/instances/"+instanceID, "")
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

// requireWorkStartedGrew asserts the node's work_started count grew past
// `baseline` within the deadline — unambiguous proof the cascade re-ran
// the node on this fire (a fresh dispatch). Fails hard on timeout.
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

// deploySensorCascadeTemplate POSTs the sensor-cascade template and
// deploys it. The template wires:
//   - a publisher `watcher` (kind http) whose config points at the
//     watched URL with poll_interval 1s; its target_node is the reactor,
//     so the emitted envelope's target == reactor.
//   - the `reactor` node subscribing (instance-scoped, frame: in) to
//     `message/invalidate/publisher/reactor` — the exact signal type the
//     delivered envelope produces (kind=invalidate, sender_kind=publisher,
//     target=reactor).
//   - the `bystander` node subscribing to a different kind
//     (`message/refresh/publisher/reactor`) so the invalidate envelope
//     never matches it (negative control).
func deploySensorCascadeTemplate(t *testing.T, ep harness.RimskyEndpoint, watchedURL string) string {
	t.Helper()

	// resolved_config for the http sensor: url + a fast poll interval +
	// match.status so any 200 matches; the body-hash watermark drives the
	// change detection.
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
			"name":                  "sensor-cascade",
			"version":               "1",
			"frame_resolution_mode": "serial_queue",
			"frame_timeout_ms":      600000,
			"nodes": []map[string]any{
				{
					"type":     reactorTargetNode,
					"executor": "stub",
					"subscribes": []map[string]any{
						{
							"instance": true,
							"type":     "message/invalidate/publisher/" + reactorTargetNode,
							"frame":    "in",
						},
					},
				},
				{
					"type":     "bystander",
					"executor": "stub",
					"subscribes": []map[string]any{
						{
							"instance": true,
							// Different message kind — the invalidate
							// envelope never produces this signal type, so
							// this node must never go stale on the sensor
							// fire. (Negative control.)
							"type":  "message/refresh/publisher/" + reactorTargetNode,
							"frame": "in",
						},
					},
				},
			},
			"publishers": []map[string]any{
				{
					"name":         "watcher",
					"kind":         "http",
					"config":       json.RawMessage(configBytes),
					"target_node":  reactorTargetNode,
					"message_kind": "invalidate",
				},
			},
		},
	}

	status, raw := ep.PostJSON(t, "/templates", body)
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
	deployStatus, deployRaw := ep.PostJSON(t, "/templates/"+resp.TemplateID+"/deploy", map[string]any{})
	if deployStatus != http.StatusOK {
		t.Fatalf("POST /templates/%s/deploy: %d %s", resp.TemplateID, deployStatus, string(deployRaw))
	}
	return resp.TemplateID
}

// createSensorCascadeInstance POSTs a new instance and returns its id.
// Creating the instance fires StartPublisherSubscriptionsForInstance,
// which inserts the rimsky_publisher_subscriptions row and calls the
// real sensor's Subscribe RPC with the resolved url.
func createSensorCascadeInstance(t *testing.T, ep harness.RimskyEndpoint, templateID, instanceKey string) string {
	t.Helper()
	status, raw := ep.PostJSON(t, "/instances", map[string]any{
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

// sensorNodeStateResponse is the shape of GET
// /v1/observability/nodes/{instance_id}/{node_type}.
type sensorNodeStateResponse struct {
	Node struct {
		State string `json:"state"`
	} `json:"node"`
	Events []struct {
		Kind string `json:"kind"`
	} `json:"events"`
}

// waitForSensorNodeState polls the node-state observability route until
// the node reaches want (or a deadline). Fails hard on timeout — never
// a skip.
func waitForSensorNodeState(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType, want string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	var lastState string
	for time.Now().Before(end) {
		status, raw := ep.GetJSON(t, "/v1/observability/nodes/"+instanceID+"/"+nodeType, "")
		if status == http.StatusOK {
			var resp sensorNodeStateResponse
			if err := json.Unmarshal(raw, &resp); err == nil {
				lastState = resp.Node.State
				if lastState == want {
					return
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("node %q on instance %s did not reach %q within %v; last state=%q",
		nodeType, instanceID, want, deadline, lastState)
}

// requireReactorReran asserts the reactor went through a SECOND run as a
// result of the sensor's emit: it counts the reactor's `work_started`
// events (emitted once per real dispatch) and requires the count to grow
// past the count observed at the moment the external change fired. A
// growth from N to N+1 is unambiguous proof the cascade re-ran the node;
// merely observing the terminal `fresh` it was already in would be a
// tautology. Fails hard on timeout.
func requireReactorReran(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string, deadline time.Duration) {
	t.Helper()
	baseline := workStartedCount(t, ep, instanceID, nodeType)
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if workStartedCount(t, ep, instanceID, nodeType) > baseline {
			// Confirm it also settled back to fresh after the re-run.
			waitForSensorNodeState(t, ep, instanceID, nodeType, "fresh", 30*time.Second)
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("reactor node %q did not re-run after the real external change within %v "+
		"(work_started count stayed at %d) — the sensor→emit→message-delivery→cascade "+
		"loop did not fire the downstream node", nodeType, deadline, baseline)
}

// workStartedCount returns the number of `work_started` events the node
// has emitted — one per real supervisor dispatch.
func workStartedCount(t *testing.T, ep harness.RimskyEndpoint, instanceID, nodeType string) int {
	t.Helper()
	status, raw := ep.GetJSON(t, "/v1/observability/nodes/"+instanceID+"/"+nodeType, "")
	if status != http.StatusOK {
		return 0
	}
	var resp sensorNodeStateResponse
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

// requirePublisherMessagePersisted asserts a message row persisted for
// the instance with sender_kind=publisher and sender == wantSender (the
// publisher_name, derived by rimsky from the publisher-subscription row —
// NOT the request body's `sender`, which the sensor sets to "sensor-http").
// Reads via the real GET /instances/{id}/messages surface so the
// assertion exercises the persisted, trust-derived sender.
func requirePublisherMessagePersisted(t *testing.T, ep harness.RimskyEndpoint, instanceID, wantSender string) {
	t.Helper()
	end := time.Now().Add(60 * time.Second)
	var lastSeen string
	for time.Now().Before(end) {
		status, raw := ep.GetJSON(t,
			"/instances/"+instanceID+"/messages?sender_kind=publisher", "")
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
						t.Fatalf("publisher message persisted with sender=%q, want %q "+
							"(rimsky must derive sender from the publisher-subscription's "+
							"publisher_name, not the request body's sender)", m.Sender, wantSender)
					}
					// Found a publisher message with the derived sender.
					return
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("no message with sender_kind=publisher persisted for instance %s within deadline; "+
		"last seen=%q — the real sensor never emitted into rimsky", instanceID, lastSeen)
}

// waitForDispatchQuiescent polls a node's work_started count until it
// stops growing for a stability window (the node has gone idle). Used to
// drain initial-frame activity before firing the sensor so any later
// growth is attributable to the sensor alone. Fails hard if the node
// never goes quiescent within the deadline (a perpetually-rerunning node
// is itself a defect).
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

// requireBystanderDidNotReRun asserts the negative-control node did NOT
// re-run on the sensor fire: its work_started count stays at the
// quiescent baseline across a settle window. Because the sensor emits an
// `invalidate` envelope and the bystander subscribes only to a `refresh`
// topic, a growth past baseline means the cascade fired spuriously (a
// bug). Waits the full window so a late spurious cascade is still caught.
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

// hostPortOf parses the integer host port out of an httptest.Server URL
// (e.g. "http://127.0.0.1:54321").
func hostPortOf(t *testing.T, rawURL string) int {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse httptest URL %q: %v", rawURL, err)
	}
	p, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("parse port from %q: %v", rawURL, err)
	}
	return p
}
