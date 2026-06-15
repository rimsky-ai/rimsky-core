// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// sensor_http_e2e_test.go is the cross-stack proof for STORY-sensor-http:
// an operator wiring a poll-driven HTTP message into a workflow uses the
// bundled sensor-http to poll a URL at a fixed interval, emit a message
// when the upstream returns success (filtered by response body), and
// persist polling state so a restart preserves the schedule + watermark.
//
// The Falsifier brief for this story is:
//
//   - Polling skips a window → refuted by asserting that mutating the
//     upstream body causes a new publisher message to land within a
//     bounded multiple of the poll interval (1s). The test fires
//     multiple body mutations and asserts each produces exactly one
//     more emit, so a sensor that drops a poll tick would either
//     deadline-out (one mutation never fires) or under-count.
//
//   - The body filter is declared but unused → refuted by configuring
//     the publisher with match.jsonpath = {path: "deployment.status",
//     value: "healthy"}, starting the upstream with a body whose value
//     at that path is "pending", and asserting NO publisher message
//     lands across multiple poll intervals. A sensor that ignores the
//     filter would emit on the first 200 OK and break the gate.
//
//   - A process restart drops the polling watermark → refuted by
//     stopping the sensor container AFTER a matching body has caused
//     exactly one emit, then bringing up a fresh container against the
//     SAME state DSN. The fresh sensor's AttachStateDB rebuilds the
//     watch from durable rows (subscription_id, url, poll interval,
//     body filter, LAST HASH). With the upstream body still set to the
//     prior matching value, the first post-restart poll observes the
//     SAME content-hash and skips emission. The test then asserts NO
//     additional publisher message arrives across multiple post-restart
//     poll intervals (the watermark survived). A sensor that lost the
//     watermark would re-emit on the first post-restart poll, growing
//     the message count and breaking the gate.
//
// The test boots a real rimsky-all-in-one against a real Postgres,
// brings up a real rimsky-sensor-http container against a SEPARATE
// Postgres for its state DSN, deploys a template declaring an http
// publisher with a body filter, and exercises all three falsifier prongs
// against the real assembled product end to end.
//
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

// httpPublisherName is both the rimsky.yml publisher key (the name
// rimsky uses to dial the sensor peer) and the template's
// publishers[].name (the row in rimsky_publisher_subscriptions that
// rimsky derives `sender` from on POST /instances/{id}/messages).
const httpPublisherName = "watcher"

// httpReactorNode is the template's reactor node — the
// publisher_subscription's target_node. The sensor's emitted envelope
// carries target=<this>, and the persisted message rows surface it as
// `target_node` we can filter on through GET /v1/instances/{id}/messages.
const httpReactorNode = "reactor"

// httpPollIntervalConfig is the resolved_config.poll_interval the
// template declares. The test's bounded waits are sized as multiples of
// this — a 5x window is enough to observe several poll ticks without
// extending the test suite's wall-clock past CI deadlines.
const httpPollIntervalConfig = "1s"
const httpPollInterval = 1 * time.Second

// TestSensorHttp_BodyFilterAndDurableWatermark is the cross-stack
// STORY-sensor-http acceptance proof. See the file doc for the falsifier
// argument the test refutes.
func TestSensorHttp_BodyFilterAndDurableWatermark(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// @deliberate: Host-side watched source. The body is mutable under a mutex so
	// the test can fire the "real external change" by swapping it
	// between matching / non-matching JSON shapes. Initial body's
	// deployment.status is "pending" — does NOT match the body
	// filter the template configures (value="healthy"), so a sensor
	// that honored the filter would NOT emit while this body is in
	// place. A request counter lets the test confirm the sensor IS
	// actually polling (refutes "polling skips a window" silently).
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

	// @deliberate: Shared docker network + a sibling Postgres for sensor-http's
	// state DSN. BringUpRimsky brings up its own `rimsky-pg`
	// Postgres for rimsky's schema; the sensor-http uses a SEPARATE
	// Postgres testcontainer on the same network so the sensor's
	// durable state survives the sensor's Terminate + fresh
	// container — exactly the durability isolation the watermark
	// proof requires.
	netName := harness.NewNetwork(ctx, t)
	statePG := startSensorHTTPStatePostgres(ctx, t, netName)

	// @constraint: Bring up the sensor BEFORE rimsky so rimsky's eager-Dial of
	// the declared publisher at startup succeeds. The sensor sits on
	// the network at alias `sensor-http` with the durable state DSN
	// wired in and host-port-access opened for the host-side
	// httptest.Server.
	sensor := harness.StartSensorHTTPHandle(ctx, t, netName, "sensor-http", statePG.internalDSN, hostPort)

	// @deliberate: A stub executor so the reactor node has somewhere to dispatch
	// when an invalidate message lands. The reactor's actual run is
	// not under test here — the proof axis is the PERSISTED publisher
	// message — but the executor declaration keeps registration on
	// the strict (default) ref_validation_mode.
	execEP := harness.StartExecutorStubOnNetwork(ctx, t, netName, "exec-ok")

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("stub", execEP),
		harness.WithPublisher(httpPublisherName, sensor.Endpoint),
	)

	// @constraint: The watched URL the sensor polls is per-subscription
	// (resolved_config.url). Inside the sensor container the
	// host-side httptest.Server is reachable via the host-gateway
	// alias.
	watchedURL := fmt.Sprintf("http://host.testcontainers.internal:%d/", hostPort)

	templateID := deploySensorHttpTemplate(t, ep, watchedURL)
	instanceID := createSensorHttpInstance(t, ep, templateID, "ck-sensor-http-e2e")

	// @constraint: Wait on OBSERVABLE subscription state — mounting is asynchronous
	// (instance-create returns 201 with the row in `mounting`; the
	// reconciler drives Subscribe to `active`), so the upstream-poll
	// wait below must not race the mount under load: the sensor only
	// starts polling once Subscribe lands.
	ep.WaitForSubscriptionsActive(t, instanceID, 90*time.Second)

	// @story: sensor-http — body filter is honored (Falsifier prong 2).
	// The upstream body has deployment.status="pending"; the template's
	// match.jsonpath filter requires deployment.status="healthy". With
	// the body NOT matching, the sensor MUST NOT emit, even as polling
	// proceeds normally. We wait several poll intervals and assert (a)
	// the sensor IS hitting the upstream (pollHits grew) and (b) NO
	// publisher message has landed in rimsky.
	waitForUpstreamPolls(t, &pollHits, 3, 30*time.Second)
	requirePublisherMessageCount(t, ep, instanceID, 0, "body-filter-not-matching")

	// @story: sensor-http — body change with matching filter causes an emit
	// (Falsifier prong 1: polling does not skip a window).
	// Swap the upstream body so deployment.status="healthy" — the
	// filter now matches. Wait until exactly one publisher message
	// lands in rimsky, bounded by a multiple of the poll interval so a
	// sensor that dropped a poll tick would deadline-out.
	bodyMu.Lock()
	body = `{"deployment":{"status":"healthy","gen":1}}`
	bodyMu.Unlock()

	requirePublisherMessagePersistedHTTP(t, ep, instanceID, httpPublisherName,
		20*time.Second, "first-match-after-filter-flip")
	// @deliberate: Pin the exact count so a sensor that emits MULTIPLE times on a
	// single body change (no watermark) breaks the gate too.
	requirePublisherMessageCountStable(t, ep, instanceID, 1,
		5*httpPollInterval, "exactly-one-after-first-match")

	// @deliberate: Independently verify the durable row carries the body-hash
	// watermark. The sensor writes LastHash on every successful emit
	// (sensor.go::pollOne → state.UpdateLastHash); the row's
	// last_hash column is the load-bearing watermark the restart
	// will rely on.
	statePool := connectSensorHTTPStatePostgres(ctx, t, statePG.hostDSN)
	defer statePool.Close()
	subID, originalLastHash := waitForSensorHttpRowWithHash(t, ctx, statePool, 20*time.Second)
	t.Logf("sensor-http persisted subscription %s with last_hash=%s before restart",
		subID, originalLastHash)

	// @story: sensor-http — restart preserves the watermark (Falsifier prong 3).
	// Stop the sensor container; rimsky stays up. With rimsky's
	// ResyncPublisherSubscriptions running only at control-api startup
	// (NOT periodically), no fresh Subscribe will be re-issued to the
	// new sensor container. Watch recovery is solely the sensor's job:
	// AttachStateDB MUST rebuild the in-memory watch from the durable
	// row (subscription_id, url, poll interval, body filter, LAST HASH)
	// or no polling resumes at all.
	preRestartCount := publisherMessageCount(t, ep, instanceID)
	preRestartPolls := pollHits.Load()
	sensor.Stop(ctx)
	t.Logf("sensor-http stopped; pre-restart message count=%d, upstream polls=%d",
		preRestartCount, preRestartPolls)

	// @deliberate: Give the host server a quiet window so post-restart polls are
	// unambiguously attributable to the revived sensor. With the sensor
	// down, pollHits MUST NOT grow.
	time.Sleep(2 * httpPollInterval)
	if got := pollHits.Load(); got > preRestartPolls {
		t.Fatalf("upstream was polled (%d → %d) while sensor was stopped — the polling "+
			"is not happening from inside the sensor process", preRestartPolls, got)
	}

	sensor.Restart(ctx)
	postRestartAt := time.Now().UTC()
	t.Logf("sensor-http restarted at %s; recovered watermark should suppress re-emit",
		postRestartAt.Format(time.RFC3339Nano))

	// @deliberate: The load-bearing observable: the body has NOT changed since
	// the first emit, so the post-restart poll observes the SAME
	// content-hash. If the watermark was recovered, the sensor MUST
	// NOT re-emit. We assert (a) the sensor IS polling again
	// (pollHits grew past preRestartPolls), and (b) the message
	// count stays pinned at preRestartCount across a stability
	// window of several poll intervals.
	waitForUpstreamPolls(t, &pollHits, int(preRestartPolls)+3, 30*time.Second)
	requirePublisherMessageCountStable(t, ep, instanceID, preRestartCount,
		5*httpPollInterval, "watermark-suppressed-re-emit-after-restart")

	// @story: sensor-http — recovered sensor is fully live (the filter, the
	// polling cadence, and the emit path all survived). Mutate the
	// body to a NEW matching shape (different content-hash, still
	// matches the filter); the revived sensor MUST observe the
	// change and emit exactly one more message. This is the cross-
	// check that the post-restart no-emit was due to the watermark
	// and not due to the sensor being broken end-to-end after
	// restart.
	bodyMu.Lock()
	body = `{"deployment":{"status":"healthy","gen":2}}`
	bodyMu.Unlock()

	requirePublisherMessageCountReaches(t, ep, instanceID, preRestartCount+1,
		20*time.Second, "second-match-after-restart")
	requirePublisherMessageCountStable(t, ep, instanceID, preRestartCount+1,
		5*httpPollInterval, "exactly-one-after-second-match")

	// @deliberate: Watermark advanced past the original. The durable row's
	// last_hash MUST differ from the pre-restart hash — the second
	// emit overwrote it through state.UpdateLastHash.
	requireSensorHttpHashAdvanced(t, ctx, statePool, subID, originalLastHash, 20*time.Second)
}

// sensorHttpStatePostgres bundles a fresh Postgres testcontainer dedicated
// to sensor-http's RIMSKY_SENSOR_HTTP_STATE_DSN. internalDSN is the in-
// network DSN the sensor container dials (host=`sensor-http-pg`);
// hostDSN is the host-mapped DSN the test process uses to assert against
// the same data.
type sensorHttpStatePostgres struct {
	internalDSN string
	hostDSN     string
}

// startSensorHTTPStatePostgres brings up a sibling postgres:15-alpine on
// the shared network with a stable alias the sensor's state DSN can
// resolve. The container is t.Cleanup-tied; the sensor-http's restart
// must NOT terminate it (durable state has to survive the sensor's
// death), and the test does not Terminate it early.
func startSensorHTTPStatePostgres(ctx context.Context, t *testing.T, networkName string) sensorHttpStatePostgres {
	t.Helper()
	dsn, hostDSN := harness.StartFreshPostgresWithAlias(ctx, t, networkName, "sensor-http-pg")
	return sensorHttpStatePostgres{internalDSN: dsn, hostDSN: hostDSN}
}

// connectSensorHTTPStatePostgres opens a pgxpool against the host-mapped
// DSN so the test process can poll sensor-http's state table.
func connectSensorHTTPStatePostgres(ctx context.Context, t *testing.T, hostDSN string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(ctx, hostDSN)
	if err != nil {
		t.Fatalf("connect sensor-http state postgres: %v", err)
	}
	return pool
}

// waitForSensorHttpRowWithHash polls the sensor's state DB until a row
// appears in sensor_http_state with a non-empty last_hash (i.e. the
// sensor has emitted at least once and persisted its watermark),
// returning the subscription id and the persisted hash. Used as the
// load-bearing pre-restart watermark snapshot.
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

// requireSensorHttpHashAdvanced asserts the persisted last_hash advanced
// to a value different from the original after the post-restart emit.
// The UpdateLastHash path runs in pollOne on every successful emit; a
// non-advanced row would mean the post-restart emit either did not run
// or did not persist its forward progress (both durability bugs). The
// poll is bounded.
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

// waitForUpstreamPolls blocks until the upstream HTTP server has been
// hit at least `min` times, or the deadline expires. Used to confirm
// the sensor IS actually polling on its declared interval (refutes a
// silent "polling skips a window" failure where the sensor stops
// polling entirely).
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

// publisherMessageCount returns the number of sender_kind=publisher
// messages persisted for the instance. Read through the real GET
// /v1/instances/{id}/messages surface so the count reflects what
// rimsky's dedup + persistence path actually stored.
func publisherMessageCount(t *testing.T, ep harness.RimskyEndpoint, instanceID string) int {
	t.Helper()
	status, raw := ep.GetJSON(t,
		"/v1/instances/"+instanceID+"/messages?sender_kind=publisher", "")
	if status != http.StatusOK {
		t.Fatalf("GET /instances/%s/messages: %d %s", instanceID, status, string(raw))
	}
	var resp struct {
		Messages []struct {
			Kind       string `json:"kind"`
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

// requirePublisherMessageCount asserts the current publisher message
// count equals `want`. Used immediately (single-shot), e.g. to assert
// the filter blocked emission when no body change has happened yet.
func requirePublisherMessageCount(t *testing.T, ep harness.RimskyEndpoint, instanceID string, want int, label string) {
	t.Helper()
	got := publisherMessageCount(t, ep, instanceID)
	if got != want {
		t.Fatalf("publisher message count for %s = %d, want %d (%s)",
			instanceID, got, want, label)
	}
}

// requirePublisherMessageCountReaches polls until the publisher message
// count reaches `want`, or fails on deadline. Used to wait for emits
// that the test expects to happen "eventually within bounded time."
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

// requirePublisherMessageCountStable polls the publisher message count
// across the given stability window and asserts it stays pinned at
// `want`. Used to confirm "no new emits" — e.g. the body filter
// blocked emission, or the recovered watermark suppressed re-emit.
// Fails fast if the count moves off `want` in either direction.
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

// requirePublisherMessagePersistedHTTP polls until a publisher message
// with sender=wantSender lands in rimsky, asserting the derived
// `sender` matches the publisher_name (rimsky overwrites `sender` on
// the server side from the publisher-subscription row, NOT the request
// body's `sender` which the sensor sets to "sensor-http"). Fails hard
// on deadline.
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
					Kind       string `json:"kind"`
					Sender     string `json:"sender"`
					SenderKind string `json:"sender_kind"`
				} `json:"messages"`
			}
			if err := json.Unmarshal(raw, &resp); err == nil {
				for _, m := range resp.Messages {
					lastSeen = fmt.Sprintf("kind=%s sender=%s sender_kind=%s",
						m.Kind, m.Sender, m.SenderKind)
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

// deploySensorHttpTemplate POSTs the sensor-http body-filter template
// and deploys it. The template wires:
//
//   - a publisher `watcher` (kind http) with poll_interval=1s and a
//     match.jsonpath filter requiring deployment.status="healthy". The
//     filter is the LOAD-BEARING under-test piece for the falsifier's
//     "body filter declared but unused" prong.
//   - a `reactor` node subscribing to the invalidate envelope topic so
//     the cascade has a real target. The reactor uses the `stub`
//     executor declared on the harness side; the proof axis here is the
//     PERSISTED publisher message in rimsky, not the reactor's run.
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
	body := map[string]any{
		"spec": map[string]any{
			"name":                  "sensor-http-e2e",
			"version":               "1",
			"frame_resolution_mode": "serial_queue",
			"frame_timeout_ms":      600000,
			"nodes": []map[string]any{
				{
					"type":     httpReactorNode,
					"executor": "stub",
					"subscribes": []map[string]any{
						{
							"instance": true,
							"type":     "message/invalidate/publisher/" + httpReactorNode,
							"frame":    "in",
						},
					},
				},
			},
			"publishers": []map[string]any{
				{
					"name":         httpPublisherName,
					"kind":         "http",
					"config":       json.RawMessage(configBytes),
					"target_node":  httpReactorNode,
					"message_kind": "invalidate",
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

// createSensorHttpInstance POSTs a new instance for the body-filter
// template and returns its id. The POST triggers rimsky's
// StartPublisherSubscriptionsForInstance which generates the
// publisher_subscription_id and calls Subscribe on the sensor peer —
// which is where the durable row first lands in sensor-http's state DB.
func createSensorHttpInstance(t *testing.T, ep harness.RimskyEndpoint, templateID, instanceKey string) string {
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
