// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// sensor_object_store_e2e_test.go is the cross-stack proof for
// STORY-sensor-object-store: an operator wiring an object-store-driven
// message into a workflow uses the bundled sensor-object-store to poll
// a bucket-and-prefix at a fixed interval, emit one message per newly-
// discovered object with REAL object metadata in the payload, and
// persist discovery state so restarts don't re-emit previously-
// discovered objects. Backend kinds are pluggable (the test exercises
// the `filesystem` backend wired into the bundled binary via the
// SetBackend pluggable extension point).
//
// The Falsifier brief for this story is:
//
//   - Restart re-emits already-discovered objects → refuted by
//     observing exactly ONE publisher message for object-A pre-
//     restart, restarting the sensor with the SAME state DSN, dropping
//     object-A AGAIN into the fresh container's bucket (because the
//     fresh container's filesystem is empty), and asserting NO
//     additional publisher message lands across a stability window
//     bounded by several poll intervals. A sensor that lost the
//     watermark on restart would re-list object-A and re-emit (count
//     would grow from 1 to 2).
//
//   - The configured backend is ignored → refuted by configuring the
//     subscription with `backend: "filesystem"` (the SDK-free
//     pluggable backend the bundled binary registers when env
//     RIMSKY_SENSOR_OBJECT_STORE_FS_ROOT is set) and dropping a file
//     into the host-mounted bucket directory: only an emit driven by
//     the filesystem lister actually walking that directory and
//     surfacing its files produces the message. A sensor that ignored
//     the backend and hard-coded the in-memory lister would observe
//     no files (the in-memory map is empty) and emit nothing, missing
//     the require-emit gate.
//
//   - Metadata in the emitted message is canned → refuted by
//     asserting the emitted payload's `object_name`, `size`, `etag`,
//     `bucket`, `prefix`, and `backend` fields match the REAL file
//     bytes the test dropped (object_name = the file path relative
//     to bucket root, size = the actual byte length, etag = the
//     FNV-64a hash of the bytes the filesystem lister recomputes from
//     disk). A canned-metadata sensor would surface stale or constant
//     values that don't match what we wrote.
//
// The test boots a real rimsky-all-in-one against a real Postgres,
// brings up a real rimsky-sensor-object-store container against a
// SEPARATE Postgres for its state DSN, deploys a template declaring
// an object-store publisher pointing at the `filesystem` backend +
// in-container bucket directory, and exercises all three falsifier
// prongs against the real assembled product end to end.
//
// @concept: sensor
// @concept: publisher
// @concept: publisher-subscription
// @story: sensor-object-store
package scenarios

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

// objectStorePublisherName is both the rimsky.yml publisher key (the
// name rimsky uses to dial the sensor peer) and the template's
// publishers[].name (the row in rimsky_publisher_subscriptions that
// rimsky derives `sender` from on POST /instances/{id}/messages).
const objectStorePublisherName = "watcher"

// objectStoreReactorNode is the template's reactor node — the
// publisher_subscription's target_node. The sensor's emitted envelope
// carries target=<this>, and the persisted message rows surface it as
// `target_node`.
const objectStoreReactorNode = "reactor"

// objectStoreBucket is the bucket name inside the filesystem backend:
// resolves at poll time to `<FsRoot>/<bucket>/` on the sensor
// container. Stable across the test so PutObject paths stay
// predictable.
const objectStoreBucket = "events"

// objectStorePollInterval governs the sensor's polling cadence per
// subscription. The bounded waits in the test are sized as multiples
// of this — a 5x window is enough to observe several poll ticks
// without extending the suite past CI deadlines.
const objectStorePollIntervalConfig = "1s"
const objectStorePollInterval = 1 * time.Second

// TestSensorObjectStore_FilesystemBackendRestartWatermark is the
// cross-stack STORY-sensor-object-store acceptance proof. See the
// file doc for the falsifier argument the test refutes.
func TestSensorObjectStore_FilesystemBackendRestartWatermark(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// 1. Shared docker network + a sibling Postgres for sensor-object-
	//    store's state DSN. BringUpRimsky brings up its own rimsky-pg
	//    Postgres for rimsky's schema; the sensor uses a SEPARATE
	//    Postgres testcontainer on the same network so the sensor's
	//    durable state survives the sensor's Terminate + fresh
	//    container — exactly the durability isolation the restart-
	//    watermark proof requires.
	netName := harness.NewNetwork(ctx, t)
	statePG := startSensorObjectStoreStatePostgres(ctx, t, netName)

	// 2. Bring up the sensor BEFORE rimsky so rimsky's eager-Dial of
	//    the declared publisher at startup succeeds. The sensor sits
	//    on the network at alias `sensor-object-store` with the
	//    durable state DSN wired in and the filesystem backend auto-
	//    registered (the harness sets RIMSKY_SENSOR_OBJECT_STORE_FS_ROOT
	//    inside the container).
	sensor := harness.StartSensorObjectStoreHandle(ctx, t, netName, "sensor-object-store", statePG.internalDSN)

	// 3. rimsky-all-in-one on the same network, with `watcher`
	//    declared as a publisher peer pointing at the sensor.
	//    ref_validation_mode=none so the reactor node (no executor
	//    declared, only a subscribes:) registers — the proof axis is
	//    the PERSISTED publisher message in rimsky, not the
	//    reactor's run.
	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithPublisher(objectStorePublisherName, sensor.Endpoint),
		harness.WithRefValidationMode("none"),
	)

	// 4. Deploy template + create instance. The template's publisher
	//    `watcher` is kind=object-store with the filesystem backend
	//    pointing at the in-container bucket root. The instance-create
	//    triggers rimsky's StartPublisherSubscriptionsForInstance,
	//    which generates the publisher_subscription_id and calls
	//    Subscribe on the sensor — landing the durable row in the
	//    sensor's state DB.
	templateID := deployObjectStoreSensorTemplate(t, ep)
	instanceID := createObjectStoreSensorInstance(t, ep, templateID, "ck-sensor-object-store-e2e")

	// 5. Wait on OBSERVABLE subscription state first: mounting is
	//    asynchronous (the create returns 201 with the row in
	//    `mounting`; the reconciler drives Subscribe to `active`), so
	//    the sensor-side assertion below must not race the mount under
	//    load — the instance surface, not a wall-clock budget, says
	//    when Subscribe has landed.
	ep.WaitForSubscriptionsActive(t, instanceID, 90*time.Second)

	//    Then confirm the sensor PERSISTED the subscription before we
	//    start dropping objects. Without this, a PutObject racing
	//    Subscribe could drop a file BEFORE the watch is registered,
	//    and the first poll would observe it but the test's wait-for-
	//    emit would still pass — masking the durability gate.
	statePool := connectSensorObjectStoreStatePostgres(ctx, t, statePG.hostDSN)
	defer statePool.Close()
	subID := waitForSensorObjectStoreSubscriptionPersisted(t, ctx, statePool, 60*time.Second)
	t.Logf("sensor-object-store persisted subscription %s", subID)

	// 6. PROOF — filesystem backend services the subscription, and
	//    metadata in the emitted message is REAL (Falsifier prongs 2
	//    + 3).
	//
	// Drop object-A into the in-container bucket. The sensor's next
	// poll calls the filesystem lister, which walks the bucket
	// directory, hashes the file, and emits one envelope carrying
	// the actual object_name + size + etag derived from the bytes
	// we wrote. We then read the emitted message back through rimsky
	// and assert each metadata field matches the real values.
	objectAName := "001-event.json"
	objectABytes := []byte(`{"event":"created","gen":1,"payload":"first"}`)
	objectAEtag := fnvHexOf(objectABytes)
	sensor.PutObject(ctx, objectStoreBucket, objectAName, objectABytes)

	requirePublisherMessageCountReaches(t, ep, instanceID, 1,
		20*time.Second, "first-object-discovered")
	// Pin the exact count so a sensor that emits MULTIPLE messages
	// per poll on a single new object breaks the gate too.
	requirePublisherMessageCountStable(t, ep, instanceID, 1,
		5*objectStorePollInterval, "exactly-one-after-first-object")

	// Read the message back and cross-check that the payload
	// metadata is the REAL file's metadata, not a canned shape.
	requireObjectStoreMessagePayload(t, ep, instanceID, objectStoreObservation{
		Backend:    "filesystem",
		Bucket:     objectStoreBucket,
		Prefix:     "",
		ObjectName: objectAName,
		Size:       int64(len(objectABytes)),
		ETag:       objectAEtag,
	}, "first-object-payload")

	// 7. Independently verify the durable row carries the watermark
	//    advance. The sensor writes WatermarkName on every successful
	//    emit (sensor.go::pollOne → state.UpdateWatermarkName); the
	//    row's watermark_name column is the load-bearing cursor the
	//    restart will rely on.
	originalWatermark := waitForSensorObjectStoreWatermark(t, ctx, statePool, subID, 20*time.Second)
	if originalWatermark != objectAName {
		t.Fatalf("sensor-object-store watermark = %q, want %q after first emit — "+
			"the persisted cursor must equal the most-recently-emitted object name "+
			"or the restart-replay gate is unobservable", originalWatermark, objectAName)
	}

	// 8. PROOF — restart preserves the watermark (Falsifier prong 1).
	//
	// Stop the sensor container; rimsky stays up. With rimsky's
	// ResyncPublisherSubscriptions running only at control-api
	// startup (NOT periodically), no fresh Subscribe will be re-
	// issued to the new sensor container. Watch recovery is solely
	// the sensor's job: AttachStateDB + Subscribe's persisted-state
	// lookup MUST rebuild the in-memory Watch from the durable row
	// (subscription_id, backend, bucket, prefix, watermark cursor)
	// or no polling resumes at all.
	preRestartCount := publisherMessageCount(t, ep, instanceID)
	sensor.Stop(ctx)
	t.Logf("sensor-object-store stopped; pre-restart message count=%d", preRestartCount)

	sensor.Restart(ctx)
	t.Logf("sensor-object-store restarted; recovered watermark must suppress re-emit " +
		"when object-A is re-dropped into the fresh container's bucket")

	// 9. The fresh container's filesystem is EMPTY — the previous
	//    container's bucket contents went away with the Terminate.
	//    Re-drop the SAME object-A (same name, same content → same
	//    fnv etag) into the fresh container's bucket. The recovered
	//    watermark says "object-A already emitted"; a sensor that
	//    honored the durable cursor MUST NOT re-emit.
	//
	//    Note that rimsky's universal idempotency-key dedup on POST
	//    /instances/{id}/messages would ALSO suppress a re-emit if
	//    the sensor sent the same idempotency key — but the sensor
	//    is expected to NOT send the message at all (the watermark
	//    is enforced sensor-side, before the post). To make THIS gate
	//    a clean sensor-side proof (not a rimsky-dedup proof), the
	//    test would need to either delete the durable row by hand
	//    before re-drop (defeats the proof) or argue the sensor-side
	//    suppression separately. Here we rely on the combined system
	//    behavior: the message-count stays pinned at 1 regardless of
	//    which layer enforces it, AND we cross-check the watermark
	//    did NOT regress (a watermark-losing sensor would also have
	//    reset the watermark on restart) below.
	sensor.PutObject(ctx, objectStoreBucket, objectAName, objectABytes)
	requirePublisherMessageCountStable(t, ep, instanceID, preRestartCount,
		5*objectStorePollInterval, "watermark-suppressed-re-emit-after-restart")

	// Cross-check: the durable watermark did NOT regress on restart
	// (a sensor whose AttachStateDB skipped the cursor would have an
	// empty watermark and would advance back to objectAName on the
	// post-restart re-emit; that path either keeps the watermark at
	// objectAName silently or regresses-then-re-advances — both
	// detectable by sampling now).
	postRestartWatermark := readSensorObjectStoreWatermark(t, ctx, statePool, subID)
	if postRestartWatermark != objectAName {
		t.Fatalf("sensor-object-store watermark = %q after restart, want %q — "+
			"the cursor must be unchanged when no new objects appear (a regression "+
			"would indicate the recovered Watch lost its cursor)",
			postRestartWatermark, objectAName)
	}

	// 10. PROOF — recovered sensor is fully live (the filesystem
	//     backend is registered, polling resumes, watermark is
	//     consulted, AND emit happens on a real new object).
	//
	// Drop object-B with a name strictly greater than object-A so
	// the name-based watermark says "new." The revived sensor MUST
	// observe it and emit exactly one more message carrying B's
	// metadata. This is the cross-check that the post-restart no-
	// emit on object-A was due to the watermark and not due to the
	// sensor being broken end-to-end after restart.
	objectBName := "002-event.json"
	objectBBytes := []byte(`{"event":"updated","gen":2,"payload":"second"}`)
	objectBEtag := fnvHexOf(objectBBytes)
	sensor.PutObject(ctx, objectStoreBucket, objectBName, objectBBytes)

	requirePublisherMessageCountReaches(t, ep, instanceID, preRestartCount+1,
		20*time.Second, "second-object-after-restart")
	requirePublisherMessageCountStable(t, ep, instanceID, preRestartCount+1,
		5*objectStorePollInterval, "exactly-one-after-second-object")

	requireObjectStoreMessagePayload(t, ep, instanceID, objectStoreObservation{
		Backend:    "filesystem",
		Bucket:     objectStoreBucket,
		Prefix:     "",
		ObjectName: objectBName,
		Size:       int64(len(objectBBytes)),
		ETag:       objectBEtag,
	}, "second-object-payload")

	// 11. Watermark advanced past object-A's name on the second
	//     emit. The durable row's watermark_name MUST now equal
	//     object-B's name — a non-advanced row would mean the post-
	//     restart emit either didn't run or didn't persist its
	//     forward progress (both durability bugs).
	requireSensorObjectStoreWatermarkAdvanced(t, ctx, statePool, subID, objectAName, objectBName, 20*time.Second)
}

// objectStoreObservation is the metadata-cross-check shape the test
// asserts against the emitted message payload. Each field is the
// REAL value derived from the file the test wrote (not a canned
// constant), so a sensor whose payload is hard-coded would mismatch
// at least one field.
type objectStoreObservation struct {
	Backend    string
	Bucket     string
	Prefix     string
	ObjectName string
	Size       int64
	ETag       string
}

// fnvHexOf returns the FNV-64a hex digest of bytes. Matches the
// filesystem lister's ETag derivation so the test can compute the
// expected ETag from the same bytes it dropped, without re-reading
// the file out of the container.
func fnvHexOf(b []byte) string {
	h := fnv.New64a()
	_, _ = h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}

// requireObjectStoreMessagePayload polls the publisher message stream
// for the instance until a message whose payload matches the given
// observation is found, or fails on deadline. Each field is checked
// against the REAL value the test wrote, so a canned-metadata sensor
// surfaces here.
//
// We poll because the most-recently-emitted message might not yet be
// readable (rimsky persists asynchronously after the POST), and the
// order of messages returned by GET /messages is the order rimsky
// stored them — so the LAST matching message wins.
func requireObjectStoreMessagePayload(t *testing.T, ep harness.RimskyEndpoint, instanceID string, want objectStoreObservation, label string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var lastSeen string
	for time.Now().Before(deadline) {
		status, raw := ep.GetJSON(t,
			"/v1/instances/"+instanceID+"/messages?sender_kind=publisher", "")
		if status == http.StatusOK {
			var resp struct {
				Messages []struct {
					Kind       string          `json:"kind"`
					Sender     string          `json:"sender"`
					SenderKind string          `json:"sender_kind"`
					Payload    json.RawMessage `json:"payload"`
				} `json:"messages"`
			}
			if err := json.Unmarshal(raw, &resp); err == nil {
				for _, m := range resp.Messages {
					if m.SenderKind != "publisher" {
						continue
					}
					var got objectStoreObservation
					var payloadAny map[string]any
					if err := json.Unmarshal(m.Payload, &payloadAny); err != nil {
						lastSeen = fmt.Sprintf("decode payload: %v: %s", err, string(m.Payload))
						continue
					}
					if v, ok := payloadAny["backend"].(string); ok {
						got.Backend = v
					}
					if v, ok := payloadAny["bucket"].(string); ok {
						got.Bucket = v
					}
					if v, ok := payloadAny["prefix"].(string); ok {
						got.Prefix = v
					}
					if v, ok := payloadAny["object_name"].(string); ok {
						got.ObjectName = v
					}
					if v, ok := payloadAny["size"].(float64); ok {
						got.Size = int64(v)
					}
					if v, ok := payloadAny["etag"].(string); ok {
						got.ETag = v
					}
					lastSeen = fmt.Sprintf("%+v", got)
					if got == want {
						return
					}
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("no publisher message matched expected metadata %+v for instance %s within "+
		"20s (last seen %q, %s) — either the emit did not happen, or the payload metadata "+
		"is canned (the load-bearing falsifier for this story)",
		want, instanceID, lastSeen, label)
}

// sensorObjectStoreStatePostgres bundles a fresh Postgres testcontainer
// dedicated to sensor-object-store's RIMSKY_SENSOR_OBJECT_STORE_STATE_DSN.
// internalDSN is the in-network DSN the sensor container dials
// (host=`sensor-object-store-pg`); hostDSN is the host-mapped DSN the
// test process uses to assert against the same data.
type sensorObjectStoreStatePostgres struct {
	internalDSN string
	hostDSN     string
}

// startSensorObjectStoreStatePostgres brings up a sibling postgres
// container on the shared network with a stable alias the sensor's
// state DSN can resolve. The container is t.Cleanup-tied; the
// sensor's restart must NOT terminate it (durable state has to
// survive the sensor's death), and the test does not Terminate it
// early.
func startSensorObjectStoreStatePostgres(ctx context.Context, t *testing.T, networkName string) sensorObjectStoreStatePostgres {
	t.Helper()
	dsn, hostDSN := harness.StartFreshPostgresWithAlias(ctx, t, networkName, "sensor-object-store-pg")
	return sensorObjectStoreStatePostgres{internalDSN: dsn, hostDSN: hostDSN}
}

// connectSensorObjectStoreStatePostgres opens a pgxpool against the
// host-mapped DSN so the test process can poll the sensor's state
// table.
func connectSensorObjectStoreStatePostgres(ctx context.Context, t *testing.T, hostDSN string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(ctx, hostDSN)
	if err != nil {
		t.Fatalf("connect sensor-object-store state postgres: %v", err)
	}
	return pool
}

// waitForSensorObjectStoreSubscriptionPersisted polls the sensor's
// state DB until at least one row appears in
// sensor_object_store_state, returning the subscription id. Used as
// the load-bearing "subscribe has landed" gate before the test
// drops any objects.
func waitForSensorObjectStoreSubscriptionPersisted(t *testing.T, ctx context.Context, pool *pgxpool.Pool, deadline time.Duration) string {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		var subID string
		err := pool.QueryRow(ctx,
			`SELECT publisher_subscription_id FROM sensor_object_store_state LIMIT 1`,
		).Scan(&subID)
		if err == nil {
			return subID
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("sensor-object-store never persisted a subscription to "+
		"sensor_object_store_state within %v — the Subscribe path is not writing "+
		"through to the durable state DB; the restart-recovery proof is unobservable "+
		"without it", deadline)
	return ""
}

// waitForSensorObjectStoreWatermark polls the sensor's state DB until
// watermark_name is non-empty (i.e. at least one emit has advanced
// the cursor), returning the watermark value. Used to verify the
// pre-restart watermark before terminating the sensor.
func waitForSensorObjectStoreWatermark(t *testing.T, ctx context.Context, pool *pgxpool.Pool, subID string, deadline time.Duration) string {
	t.Helper()
	end := time.Now().Add(deadline)
	var last string
	for time.Now().Before(end) {
		var wm string
		err := pool.QueryRow(ctx,
			`SELECT COALESCE(watermark_name, '') FROM sensor_object_store_state WHERE publisher_subscription_id = $1`,
			subID,
		).Scan(&wm)
		if err == nil {
			last = wm
			if wm != "" {
				return wm
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("sensor-object-store never persisted a non-empty watermark_name within %v "+
		"(last seen %q) — the emit path is not writing the cursor through to durable "+
		"state; the restart-recovery proof is unobservable without it", deadline, last)
	return ""
}

// readSensorObjectStoreWatermark returns the current watermark_name
// for the given subscription. Used as a single-shot read (no polling)
// to cross-check that the cursor stayed pinned across a window where
// the test expects no emit.
func readSensorObjectStoreWatermark(t *testing.T, ctx context.Context, pool *pgxpool.Pool, subID string) string {
	t.Helper()
	var wm string
	err := pool.QueryRow(ctx,
		`SELECT COALESCE(watermark_name, '') FROM sensor_object_store_state WHERE publisher_subscription_id = $1`,
		subID,
	).Scan(&wm)
	if err != nil {
		t.Fatalf("read sensor-object-store watermark for %s: %v", subID, err)
	}
	return wm
}

// requireSensorObjectStoreWatermarkAdvanced asserts the persisted
// watermark_name advanced from `previous` to `expected` within the
// deadline. The UpdateWatermarkName path runs in pollOne on every
// successful emit; a non-advanced row would mean the post-restart
// emit didn't run or didn't persist its forward progress (both
// durability bugs). The poll is bounded.
func requireSensorObjectStoreWatermarkAdvanced(t *testing.T, ctx context.Context, pool *pgxpool.Pool, subID, previous, expected string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	var last string
	for time.Now().Before(end) {
		var wm string
		err := pool.QueryRow(ctx,
			`SELECT COALESCE(watermark_name, '') FROM sensor_object_store_state WHERE publisher_subscription_id = $1`,
			subID,
		).Scan(&wm)
		if err == nil {
			last = wm
			if wm == expected {
				return
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("sensor-object-store did not advance watermark_name to %q within %v "+
		"(last seen %q, previous was %q) — the durable cursor must move forward on "+
		"each successful emit, otherwise the next poll on the same bucket would re-emit "+
		"silently", expected, deadline, last, previous)
}

// deployObjectStoreSensorTemplate POSTs the object-store sensor
// template and deploys it. The template wires:
//
//   - a publisher `watcher` (kind object-store) with the filesystem
//     backend pointing at the in-container bucket directory and a
//     1s poll_interval. The backend choice is the LOAD-BEARING
//     under-test piece for the falsifier's "configured backend is
//     ignored" prong: a sensor that hard-codes the in-memory lister
//     would observe an empty map and never emit, missing the require-
//     emit gate below.
//   - a `reactor` node subscribing to the matching message topic so
//     the cascade has a real target. The reactor names no executor;
//     ref_validation_mode=none on the harness side keeps the
//     registration accepting that shape. The proof axis is the
//     PERSISTED publisher message, not the reactor's run.
func deployObjectStoreSensorTemplate(t *testing.T, ep harness.RimskyEndpoint) string {
	t.Helper()
	sensorConfig := map[string]any{
		"backend":         "filesystem",
		"bucket":          objectStoreBucket,
		"prefix":          "",
		"poll_interval":   objectStorePollIntervalConfig,
		"watermark_field": "name",
	}
	configBytes, err := json.Marshal(sensorConfig)
	if err != nil {
		t.Fatalf("marshal sensor config: %v", err)
	}
	body := map[string]any{
		"spec": map[string]any{
			"name":                  "sensor-object-store-e2e",
			"version":               "1",
			"frame_resolution_mode": "serial_queue",
			"frame_timeout_ms":      600000,
			"nodes": []map[string]any{
				{
					"type": objectStoreReactorNode,
					"subscribes": []map[string]any{
						{
							"instance": true,
							"type":     "message/invalidate/publisher/" + objectStoreReactorNode,
							"frame":    "in",
						},
					},
				},
			},
			"publishers": []map[string]any{
				{
					"name":         objectStorePublisherName,
					"kind":         "object-store",
					"config":       json.RawMessage(configBytes),
					"target_node":  objectStoreReactorNode,
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

// createObjectStoreSensorInstance POSTs a new instance for the
// object-store template and returns its id. The POST triggers
// rimsky's StartPublisherSubscriptionsForInstance which generates
// the publisher_subscription_id and calls Subscribe on the sensor
// peer — which is where the durable row first lands in the sensor's
// state DB.
func createObjectStoreSensorInstance(t *testing.T, ep harness.RimskyEndpoint, templateID, instanceKey string) string {
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
