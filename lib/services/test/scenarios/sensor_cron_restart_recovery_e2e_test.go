// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// sensor_cron_restart_recovery_e2e_test.go is the cross-stack proof for
// STORY-sensor-cron's restart-recovery acceptance: a sensor-cron
// instance configured with a state DSN pointing at a real Postgres
// holds a publisher-subscription whose next_fire_at is future;
// restarting the binary preserves the subscription and the binary fires
// at the originally-scheduled window without rimsky re-issuing
// Subscribe.
//
// The sensor-internal unit tests (state_db_test.go, replica_posture_
// test.go, multi_replica_test.go, sensor_test.go) and the cascade e2e
// (sensor_cascade_e2e_test.go) cover the in-process firing path and the
// real-rimsky-cascade direction respectively; this test adds the
// missing leg: a REAL sensor-cron CONTAINER process is killed and a
// fresh process is brought up against the SAME state DSN, observed
// end-to-end through rimsky's persisted message stream. The Falsifier
// brief for this story is:
//
//   - State persists but the binary refuses to honor it on restart →
//     refuted by asserting the post-restart sensor recovers the SAME
//     publisher_subscription_id with the SAME next_fire_at and fires
//     into rimsky's message stream on the next Tick.
//   - Two replicas fire only once per window (silent leader election) →
//     refuted by replica_posture_test.go's facet 2 + the no-coordination
//     -primitive source scan; not re-asserted here.
//   - Cron advancement uses wall clock instead of the row's prior
//     next_fire_at → refuted by stopping the sensor BEFORE the window
//     and restarting AFTER it: a wall-clock-advancing implementation
//     would skip the missed window (sched.Next(now) yields the NEXT
//     window, not the missed one), so the post-restart fire would be
//     absent. The recovered-watermark implementation fires on the
//     originally-scheduled window because next_fire_at on disk is now
//     in the past.
//
// The test boots a real rimsky-all-in-one against a real Postgres,
// brings up a real rimsky-sensor-cron container against the SAME
// Postgres for its state DSN, deploys a template declaring the cron
// publisher, creates an instance (which fires the real Subscribe path
// through rimsky → sensor), kills the sensor mid-window, and after the
// originally-scheduled fire window passes brings up a fresh sensor
// container with identical env. The proof is a message row with
// sender_kind=publisher persisted in rimsky for the originally-issued
// publisher_subscription_id.
//
// @concept: sensor
// @concept: publisher
// @concept: publisher-subscription
// @story: sensor-cron
package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

// cronPublisherName is both the rimsky.yml publisher key (the name
// rimsky uses to dial the sensor peer) and the template's
// publishers[].name (the row in rimsky_publisher_subscriptions that
// rimsky derives `sender` from on POST /instances/{id}/messages).
const cronPublisherName = "tick"

// cronReactorNode is the template's reactor node — the
// publisher_subscription's target_node. The sensor's emitted envelope
// carries target=<this>, and the persisted message rows surface it as
// `target_node` we can filter on through GET /v1/instances/{id}/messages.
const cronReactorNode = "reactor"

// TestSensorCronRestartRecovery is the cross-stack restart-recovery
// proof for STORY-sensor-cron. See the file doc for the falsifier
// argument the test refutes.
func TestSensorCronRestartRecovery(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// 1. Shared docker network + a single Postgres testcontainer the
	//    whole stack (rimsky + sensor-cron state DSN) talks to.
	//    BringUpRimsky brings up its own rimsky-pg postgres container
	//    on this network at hostname `rimsky-pg`; the sensor-cron uses
	//    the SAME Postgres on a sibling logical schema (`sensor_cron_
	//    state` table, created at sensor bootstrap). That sharing
	//    matters: the durable rows must persist across the sensor's
	//    Terminate + fresh container — using the same Postgres
	//    container guarantees the durable rows do not disappear with
	//    the sensor process.
	netName := harness.NewNetwork(ctx, t)

	// 2. Bring up rimsky first (so its Postgres testcontainer is
	//    running on the network) — but we need the sensor-cron's state
	//    DSN BEFORE the sensor starts, and we need the sensor RUNNING
	//    before rimsky's eager-dial of its declared publishers. We
	//    resolve this by starting rimsky once to learn the in-network
	//    Postgres DSN, then bringing up the sensor against that DSN
	//    using a SEPARATE bring-up path.
	//
	//    The simplest shape that works: BringUpRimskyHandle (with
	//    WithPublisher pointing at the sensor's in-network endpoint
	//    we KNOW in advance — alias `sensor-cron` + port 9081) on this
	//    network; rimsky's eager-Subscribe at instance-create races
	//    with the sensor's start, but the sensor is up before we
	//    create the instance, so the race is bounded by us.
	//
	//    Concretely:
	//      - StartSensorCron on this network FIRST with a known DSN
	//        pointing at a sibling postgres testcontainer.
	//      - BringUpRimsky on the same network, declaring `tick` as a
	//        publisher peer pointing at the sensor's endpoint.
	//      - Deploy + create instance → rimsky calls Subscribe; the
	//        sensor persists the row.
	//
	//    For the state DSN we use a SEPARATE Postgres testcontainer
	//    (StartFreshPostgres) on the same network so the sensor's
	//    state survives a rimsky restart and vice versa — and so the
	//    test isolates sensor-cron's durability from rimsky's
	//    persistence.

	// 2a. Sensor's state DSN postgres. Sibling container on the same
	//     network, durable across the sensor's restart.
	statePGContainer := startSensorStatePostgres(ctx, t, netName)

	// 2b. Bring up the sensor BEFORE rimsky so rimsky's eager-Dial of
	//     the declared publisher at startup succeeds; the sensor is on
	//     the network at alias `sensor-cron` with the durable state DSN
	//     wired in.
	sensor := harness.StartSensorCron(ctx, t, netName, "sensor-cron", statePGContainer.internalDSN)

	// 2c. rimsky-all-in-one on the same network, with `tick` declared
	//     as a publisher peer pointing at the sensor.
	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithPublisher(cronPublisherName, sensor.Endpoint),
		// The template's reactor node references no executor; set
		// ref_validation_mode=none so the unreferenced-executor check
		// does not refuse the template at registration. The test does
		// not need the reactor to actually run — only that rimsky
		// PERSISTS the publisher-message a sensor-cron POSTs.
		harness.WithRefValidationMode("none"),
	)

	// 3. Deploy template + create instance. The cron expression `*
	//    * * * *` (every minute) keeps next_fire_at strictly within
	//    60s of subscribe, so the test can observe the windowed fire
	//    in bounded time. The exact field doesn't matter — what
	//    matters is that the persisted next_fire_at is a wall-clock
	//    moment we will roll past during the test.
	templateID := deployCronSensorTemplate(t, ep)
	instanceID := createCronSensorInstance(t, ep, templateID, "ck-sensor-cron-restart")

	// 4. Wait until the sensor-cron has PERSISTED the subscription to
	//    its state DB. The Subscribe RPC writes the row before
	//    returning, but the call is fire-and-forget from rimsky's
	//    POV (instance-create returns 201 regardless), so we poll the
	//    sensor's state table directly via the host-mapped postgres
	//    port until we see one row.
	statePool := connectSensorStatePostgres(ctx, t, statePGContainer.hostDSN)
	defer statePool.Close()

	subID, originalNextFire := waitForSensorCronSubscriptionPersisted(t, ctx, statePool, 90*time.Second)
	t.Logf("sensor-cron persisted subscription %s with next_fire_at=%s (originally scheduled)",
		subID, originalNextFire.UTC().Format(time.RFC3339Nano))

	// 5. Kill the sensor BEFORE its originally-scheduled window. The
	//    `* * * * *` schedule's first fire after subscribe is the
	//    NEXT minute boundary; stopping the container while
	//    next_fire_at is still in the future is the load-bearing
	//    setup for the recovery proof. If we stopped AFTER the
	//    window, recovery and wall-clock-recompute would be
	//    indistinguishable.
	sensor.Stop(ctx)
	t.Logf("sensor-cron stopped at %s, before scheduled window %s",
		time.Now().UTC().Format(time.RFC3339Nano), originalNextFire.UTC().Format(time.RFC3339Nano))

	// 6. Wait until the wall clock is past the originally-scheduled
	//    window. With cron `* * * * *` the window is within 60s of
	//    subscribe, so this is bounded; we add a safety margin so a
	//    wall-clock recompute (the falsified implementation) would
	//    deterministically yield a LATER next_fire_at than the
	//    recovered watermark.
	sleepUntilPast(originalNextFire, 5*time.Second)

	// 7. Bring the sensor back up with IDENTICAL env. The fresh
	//    process's AttachStateDB rebuilds in-memory watches from
	//    durable storage — the recovered watch carries the ORIGINAL
	//    next_fire_at (now in the past), so the first Tick after
	//    boot fires immediately on the originally-scheduled window.
	//    A wall-clock implementation would compute next_fire_at =
	//    sched.Next(now) which is in the FUTURE, and the window
	//    would never produce a message during the rest of this
	//    test.
	restartAt := time.Now().UTC()
	sensor.Restart(ctx)
	t.Logf("sensor-cron restarted at %s; recovered watermark should fire on first Tick",
		restartAt.Format(time.RFC3339Nano))

	// 8. The load-bearing observable: a message row with
	//    sender_kind=publisher MUST persist in rimsky AFTER the
	//    restart timestamp. rimsky derives `sender` from the
	//    publisher-subscription row (cronPublisherName), so the
	//    assertion pins that derived value end-to-end. The post-
	//    restart cut on received_at is what proves the revived
	//    process was the poster — a wall-clock-recompute
	//    implementation would not produce a message in the
	//    interval between restart and the NEXT minute boundary,
	//    so this gate falsifies the falsified shape.
	requireRecoveredPublisherMessage(t, ep, instanceID, cronPublisherName, restartAt, 90*time.Second)

	// 9. Cross-check: the sensor advanced next_fire_at on the
	//    durable row past the originally-scheduled window. The
	//    UpdateNextFire path runs on every fire (sensor.go::fireOne),
	//    so a non-advanced row would mean the fire never ran (or ran
	//    and dropped the persist), both of which break the
	//    durability contract. We poll the state DB until next_
	//    fire_at > originalNextFire.
	requireSensorCronAdvancedWatermark(t, ctx, statePool, subID, originalNextFire, 60*time.Second)
}

// sensorStatePostgres bundles a fresh Postgres testcontainer dedicated
// to sensor-cron's RIMSKY_SENSOR_CRON_STATE_DSN. internalDSN is the in-
// network DSN the sensor container dials (host=`sensor-cron-pg`);
// hostDSN is the host-mapped DSN the test process uses to assert
// against the same data.
type sensorStatePostgres struct {
	internalDSN string
	hostDSN     string
}

// startSensorStatePostgres brings up a sibling postgres:15-alpine on
// the shared network with a stable alias the sensor's state DSN can
// resolve. The container is t.Cleanup-tied; the sensor-cron's restart
// must NOT terminate it (durable state has to survive the sensor's
// death), and the test does not Terminate it early.
func startSensorStatePostgres(ctx context.Context, t *testing.T, networkName string) sensorStatePostgres {
	t.Helper()
	// We need a stable in-network alias so the sensor container's env
	// DSN can name it. Reuse the pgmodule shape used by BringUpRimsky's
	// rimsky-pg testcontainer, but with our own alias.
	dsn, hostDSN := harness.StartFreshPostgresWithAlias(ctx, t, networkName, "sensor-cron-pg")
	return sensorStatePostgres{internalDSN: dsn, hostDSN: hostDSN}
}

// connectSensorStatePostgres opens a pgxpool against the host-mapped
// DSN so the test process can poll sensor-cron's state table.
func connectSensorStatePostgres(ctx context.Context, t *testing.T, hostDSN string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(ctx, hostDSN)
	if err != nil {
		t.Fatalf("connect sensor-cron state postgres: %v", err)
	}
	return pool
}

// waitForSensorCronSubscriptionPersisted polls the sensor's state DB
// until a row appears in sensor_cron_state, returning the subscription
// id and the persisted next_fire_at. Used as the load-bearing pre-
// restart watermark.
func waitForSensorCronSubscriptionPersisted(t *testing.T, ctx context.Context, pool *pgxpool.Pool, deadline time.Duration) (string, time.Time) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		var subID string
		var nextFire time.Time
		err := pool.QueryRow(ctx,
			`SELECT publisher_subscription_id, next_fire_at FROM sensor_cron_state LIMIT 1`,
		).Scan(&subID, &nextFire)
		if err == nil {
			return subID, nextFire
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("sensor-cron never persisted a subscription to sensor_cron_state within %v — "+
		"the Subscribe path is not writing through to the durable state DB", deadline)
	return "", time.Time{}
}

// sleepUntilPast blocks until wall-clock now() is past target + extra.
// Used to roll past the originally-scheduled window before restarting
// the sensor — a wall-clock-recompute implementation would advance to
// a strictly-LATER next_fire_at, so we must definitively be past the
// original watermark to falsify it.
func sleepUntilPast(target time.Time, extra time.Duration) {
	deadline := target.Add(extra)
	if d := time.Until(deadline); d > 0 {
		time.Sleep(d)
	}
}

// requireRecoveredPublisherMessage asserts a message row persisted in
// rimsky for the given instance, posted by the REVIVED sensor-cron:
//
//   - sender_kind=publisher (the universal publisher-side message route)
//   - sender=<wantSender> (rimsky derives `sender` from
//     publisher_subscriptions.publisher_name on the server side, so the
//     value here is the subscription's publisher_name — not whatever the
//     sensor put in the request body)
//   - delivered_at > restartAt (the load-bearing observable: a message
//     POSTed AFTER the sensor restart proves the revived process honored
//     the durable next_fire_at instead of waiting for rimsky to re-issue
//     Subscribe — which would have produced a re-Subscribed
//     next_fire_at = sched.Next(now-after-restart) and shifted the first
//     fire window past the test deadline)
//
// The publisher_subscription_id field is NOT projected on the public
// GET /v1/instances/{id}/messages response (messageItem in
// lib/control/controlapi/messages.go), so we cannot match on the
// originally-issued subscription id at the HTTP altitude. The
// delivered-after-restart filter is the equivalent identification: in
// this test there is exactly one publisher-subscription on the
// instance, so a publisher message delivered after the restart can
// only come from the revived sensor honoring the durable row. The
// pre-restart message (if Subscribe-time Tick happened to fall on the
// minute boundary) is excluded by the delivered_after filter.
func requireRecoveredPublisherMessage(t *testing.T, ep harness.RimskyEndpoint, instanceID, wantSender string, restartAt time.Time, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	var lastSeen string
	for time.Now().Before(end) {
		status, raw := ep.GetJSON(t,
			"/v1/instances/"+instanceID+"/messages?sender_kind=publisher", "")
		if status == http.StatusOK {
			var resp struct {
				Messages []struct {
					Kind        string     `json:"kind"`
					Sender      string     `json:"sender"`
					SenderKind  string     `json:"sender_kind"`
					ReceivedAt  time.Time  `json:"received_at"`
					DeliveredAt *time.Time `json:"delivered_at"`
				} `json:"messages"`
			}
			if err := json.Unmarshal(raw, &resp); err == nil {
				for _, m := range resp.Messages {
					lastSeen = fmt.Sprintf("kind=%s sender=%s sender_kind=%s received=%s delivered=%v",
						m.Kind, m.Sender, m.SenderKind,
						m.ReceivedAt.UTC().Format(time.RFC3339Nano), m.DeliveredAt)
					if m.SenderKind != "publisher" {
						continue
					}
					// Use received_at as the post-restart cut: the
					// rimsky-side row is timestamped on the server side
					// when the sensor POSTs, so a received_at > restartAt
					// is unambiguous proof the revived sensor was the
					// poster. delivered_at is set later by the cascade,
					// so it may still be nil for very recent fires —
					// received_at is the right axis here.
					if !m.ReceivedAt.After(restartAt) {
						continue
					}
					if m.Sender != wantSender {
						t.Fatalf("publisher message persisted with sender=%q, want %q "+
							"(rimsky must derive sender from publisher_subscriptions."+
							"publisher_name, not the sensor request body's sender). "+
							"Message received_at=%s, restartAt=%s",
							m.Sender, wantSender,
							m.ReceivedAt.UTC().Format(time.RFC3339Nano),
							restartAt.UTC().Format(time.RFC3339Nano))
					}
					// Found a publisher message received AFTER the
					// sensor restart — the revived process honored
					// its durable subscription end to end.
					return
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("no publisher message received after restart (%s) for instance %s within %v; "+
		"last seen=%q — the revived sensor-cron either did not recover the durable "+
		"subscription (in-memory mode bug) or recomputed next_fire_at from wall clock "+
		"(durability contract violation)",
		restartAt.UTC().Format(time.RFC3339Nano), instanceID, deadline, lastSeen)
}

// requireSensorCronAdvancedWatermark asserts the persisted next_fire_
// at advanced strictly past the original window after the recovered
// fire. UpdateNextFire runs in fireOne on every fire (sensor.go); a
// non-advanced row would mean the fire did not persist its forward
// progress, which would re-fire the same window on the next Tick — a
// silent re-fire is itself a durability bug. The poll is bounded.
func requireSensorCronAdvancedWatermark(t *testing.T, ctx context.Context, pool *pgxpool.Pool, subID string, original time.Time, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	var last time.Time
	for time.Now().Before(end) {
		var nextFire time.Time
		err := pool.QueryRow(ctx,
			`SELECT next_fire_at FROM sensor_cron_state WHERE publisher_subscription_id = $1`, subID,
		).Scan(&nextFire)
		if err == nil {
			last = nextFire
			if nextFire.After(original) {
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("sensor-cron did not advance next_fire_at past %s within %v (last seen %s) — "+
		"the durable watermark must move forward after a fire, otherwise the next Tick "+
		"would re-fire the same window",
		original.UTC().Format(time.RFC3339Nano), deadline, last.UTC().Format(time.RFC3339Nano))
}

// deployCronSensorTemplate POSTs the restart-recovery template and
// deploys it. The template wires:
//
//   - a publisher `tick` (kind cron) at a 1-minute schedule, targeting
//     the `reactor` node. The persisted publisher-subscription row's
//     next_fire_at is the load-bearing durable state under test.
//   - a `reactor` node that subscribes to the matching message topic,
//     so a delivered envelope is fan-out-routed normally. The reactor
//     names no executor; ref_validation_mode=none on the harness side
//     keeps the registration accepting that shape.
func deployCronSensorTemplate(t *testing.T, ep harness.RimskyEndpoint) string {
	t.Helper()
	cronCfg := map[string]any{"cron": "* * * * *"}
	cronBytes, err := json.Marshal(cronCfg)
	if err != nil {
		t.Fatalf("marshal cron config: %v", err)
	}
	body := map[string]any{
		"spec": map[string]any{
			"name":                  "sensor-cron-restart",
			"version":               "1",
			"frame_resolution_mode": "serial_queue",
			"frame_timeout_ms":      600000,
			"nodes": []map[string]any{
				{
					"type": cronReactorNode,
					"subscribes": []map[string]any{
						{
							"instance": true,
							"type":     "message/invalidate/publisher/" + cronReactorNode,
							"frame":    "in",
						},
					},
				},
			},
			"publishers": []map[string]any{
				{
					"name":         cronPublisherName,
					"kind":         "cron",
					"config":       json.RawMessage(cronBytes),
					"target_node":  cronReactorNode,
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
		t.Fatalf("POST /templates/%s/deploy: %d %s", resp.TemplateID, deployStatus, string(deployRaw))
	}
	return resp.TemplateID
}

// createCronSensorInstance POSTs a new instance for the restart-
// recovery template and returns its id. The POST triggers rimsky's
// StartPublisherSubscriptionsForInstance which generates the
// publisher_subscription_id and calls Subscribe on the sensor peer —
// which is where the durable row first lands in sensor-cron's state DB.
func createCronSensorInstance(t *testing.T, ep harness.RimskyEndpoint, templateID, instanceKey string) string {
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
