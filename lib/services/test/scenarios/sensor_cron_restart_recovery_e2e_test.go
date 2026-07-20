// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

const cronPublisherName = "tick"

const cronReactorNode = "reactor"

const cronMessageType = "tick/reactor"

func TestSensorCronRestartRecovery(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.NewNetwork(ctx, t)

	statePGContainer := startSensorStatePostgres(ctx, t, netName)

	rimskyAlias := harness.NextRimskyAlias()
	rimskyInternalURL := fmt.Sprintf("http://%s:8080", rimskyAlias)
	sensor := harness.StartSensorCron(ctx, t, netName, "sensor-cron", rimskyInternalURL, statePGContainer.internalDSN)

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithRimskyAlias(rimskyAlias),
		harness.WithPublisher(cronPublisherName, sensor.Endpoint),
	)

	templateID := deployCronSensorTemplate(t, ep)
	instanceID := createCronSensorInstance(t, ep, templateID, "ck-sensor-cron-restart")

	ep.WaitForSubscriptionsActive(t, instanceID, 90*time.Second)

	statePool := connectSensorStatePostgres(ctx, t, statePGContainer.hostDSN)
	defer statePool.Close()

	subID, originalNextFire := waitForSensorCronSubscriptionPersisted(t, ctx, statePool, 90*time.Second)
	t.Logf("sensor-cron persisted subscription %s with next_fire_at=%s (originally scheduled)",
		subID, originalNextFire.UTC().Format(time.RFC3339Nano))

	sensor.Stop(ctx)
	t.Logf("sensor-cron stopped at %s, before scheduled window %s",
		time.Now().UTC().Format(time.RFC3339Nano), originalNextFire.UTC().Format(time.RFC3339Nano))

	sleepUntilPast(originalNextFire, 5*time.Second)

	restartAt := time.Now().UTC()
	sensor.Restart(ctx)
	t.Logf("sensor-cron restarted at %s; recovered watermark should fire on first Tick",
		restartAt.Format(time.RFC3339Nano))

	requireRecoveredPublisherMessage(t, ep, instanceID, cronPublisherName, restartAt, originalNextFire, 90*time.Second)

	requireSensorCronAdvancedWatermark(t, ctx, statePool, subID, originalNextFire, 60*time.Second)
}

type sensorStatePostgres struct {
	internalDSN string
	hostDSN     string
}

func startSensorStatePostgres(ctx context.Context, t *testing.T, networkName string) sensorStatePostgres {
	t.Helper()
	dsn, hostDSN := harness.StartFreshPostgresWithAlias(ctx, t, networkName, "sensor-cron-pg")
	return sensorStatePostgres{internalDSN: dsn, hostDSN: hostDSN}
}

func connectSensorStatePostgres(ctx context.Context, t *testing.T, hostDSN string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(ctx, hostDSN)
	if err != nil {
		t.Fatalf("connect sensor-cron state postgres: %v", err)
	}
	return pool
}

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

func sleepUntilPast(target time.Time, extra time.Duration) {
	deadline := target.Add(extra)
	if d := time.Until(deadline); d > 0 {
		time.Sleep(d)
	}
}

func requireRecoveredPublisherMessage(t *testing.T, ep harness.RimskyEndpoint, instanceID, wantSender string, restartAt, wantFireAt time.Time, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	var lastSeen string
	for time.Now().Before(end) {
		status, raw := ep.GetJSON(t,
			"/v1/instances/"+instanceID+"/messages?sender_kind=publisher", "")
		if status == http.StatusOK {
			var resp struct {
				Messages []struct {
					Type        string          `json:"type"`
					Sender      string          `json:"sender"`
					SenderKind  string          `json:"sender_kind"`
					Payload     json.RawMessage `json:"payload"`
					ReceivedAt  time.Time       `json:"received_at"`
					DeliveredAt *time.Time      `json:"delivered_at"`
				} `json:"messages"`
			}
			if err := json.Unmarshal(raw, &resp); err == nil {
				for _, m := range resp.Messages {
					lastSeen = fmt.Sprintf("type=%s sender=%s sender_kind=%s received=%s delivered=%v payload=%s",
						m.Type, m.Sender, m.SenderKind,
						m.ReceivedAt.UTC().Format(time.RFC3339Nano), m.DeliveredAt, string(m.Payload))
					if m.SenderKind != "publisher" {
						continue
					}
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
					gotFireAt := decodeFireAtPayload(t, m.Payload)
					if !gotFireAt.Truncate(time.Second).Equal(wantFireAt.UTC().Truncate(time.Second)) {
						t.Fatalf("recovered publisher message fired for window fire_at=%s, want the "+
							"durably-recovered window %s — a sensor that lost its watermark and "+
							"rescheduled from wall clock instead of recovering the durable next_fire_at "+
							"would also fire After(restartAt) but for a DIFFERENT window",
							gotFireAt.Format(time.RFC3339Nano), wantFireAt.UTC().Format(time.RFC3339Nano))
					}
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

func decodeFireAtPayload(t *testing.T, raw json.RawMessage) time.Time {
	t.Helper()
	var body struct {
		FireAt string `json:"fire_at"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode publisher message payload for fire_at: %v: %s", err, string(raw))
	}
	fireAt, err := time.Parse(time.RFC3339, body.FireAt)
	if err != nil {
		t.Fatalf("parse payload fire_at %q: %v", body.FireAt, err)
	}
	return fireAt.UTC()
}

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

func deployCronSensorTemplate(t *testing.T, ep harness.RimskyEndpoint) string {
	t.Helper()
	cronCfg := map[string]any{"cron": "* * * * *"}
	cronBytes, err := json.Marshal(cronCfg)
	if err != nil {
		t.Fatalf("marshal cron config: %v", err)
	}
	body := map[string]any{
		"spec": map[string]any{
			"name":    "sensor-cron-restart",
			"version": "1",
			"messages": []map[string]any{
				{
					"type": cronMessageType,
					"body_schema": map[string]any{
						"type":                 "object",
						"properties":           map[string]any{},
						"additionalProperties": true,
					},
				},
			},
			"nodes": []map[string]any{
				{
					"type": cronReactorNode,
					"subscribes": []map[string]any{
						{
							"node":                   cronMessageType,
							"type":                   "terminal/success",
							"force_upstream_refresh": false,
						},
					},
				},
			},
			"publishers": []map[string]any{
				{
					"name":         cronPublisherName,
					"kind":         "cron",
					"config":       json.RawMessage(cronBytes),
					"message_type": cronMessageType,
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
