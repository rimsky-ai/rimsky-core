// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
)

const cronPublisherName = "tick"

const cronReactorNode = "reactor"

const cronMessageType = "tick/reactor"

func TestSensorCronRestartRecovery(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.SharedNetworkName(ctx, t)

	statePGContainer := startSensorStatePostgres(ctx, t, netName, "sensor-cron-pg")

	rimskyAlias := harness.NextRimskyAlias()
	rimskyInternalURL := fmt.Sprintf("http://%s:8080", rimskyAlias)
	sensor := harness.StartSensorCron(ctx, t, netName, "sensor-cron", rimskyInternalURL, statePGContainer.internalDSN)
	execEndpoint := harness.StartExecutorStubOnNetwork(ctx, t, netName)

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithRimskyAlias(rimskyAlias),
		harness.WithPublisher(cronPublisherName, sensor.Endpoint),
		harness.WithExecutor("stub", execEndpoint),
	)

	templateID := deployCronSensorTemplate(t, ep)
	instanceID := createCronSensorInstance(t, ep, templateID, "ck-sensor-cron-restart")

	ep.WaitForSubscriptionsActive(t, instanceID)

	statePool := connectSensorStatePostgres(ctx, t, statePGContainer.hostDSN)
	defer statePool.Close()

	subID, originalNextFire := waitForSensorCronSubscriptionPersisted(t, ctx, statePool)
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

	requireRecoveredPublisherMessage(t, ep, instanceID, cronPublisherName, restartAt, originalNextFire)

	ep.RequireNodeTerminalSucceeded(t, instanceID, cronReactorNode)

	requireSensorCronAdvancedWatermark(t, ctx, statePool, subID, originalNextFire)
}

type sensorStatePostgres struct {
	internalDSN string
	hostDSN     string
}

func startSensorStatePostgres(ctx context.Context, t *testing.T, networkName, alias string) sensorStatePostgres {
	t.Helper()
	dsn, hostDSN := harness.StartFreshPostgresWithAlias(ctx, t, networkName, alias)
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

func waitForSensorCronSubscriptionPersisted(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (string, time.Time) {
	t.Helper()
	var subID string
	var nextFire time.Time
	awaited.Until(t, "the Subscribe path to write a subscription through to the durable sensor_cron_state", func() bool {
		return pool.QueryRow(ctx,
			`SELECT publisher_subscription_id, next_fire_at FROM sensor_cron_state LIMIT 1`,
		).Scan(&subID, &nextFire) == nil
	})
	return subID, nextFire
}

func sleepUntilPast(target time.Time, extra time.Duration) {
	deadline := target.Add(extra)
	if d := time.Until(deadline); d > 0 {
		//nolint:testwallclock-pacing advances the real clock past a cron window the sensor reads from the host clock and no test can drive; the verdict is which window the revived sensor fires for, never how long this took
		time.Sleep(d)
	}
}

func requireRecoveredPublisherMessage(t *testing.T, ep harness.RimskyEndpoint, instanceID, wantSender string, restartAt, wantFireAt time.Time) {
	t.Helper()
	awaited.Until(t, fmt.Sprintf("the revived sensor-cron to deliver a publisher message for the durably-recovered "+
		"window %s on instance %s; a sensor that lost its subscription, or recomputed next_fire_at from the wall "+
		"clock, never delivers one", wantFireAt.UTC().Format(time.RFC3339Nano), instanceID), func() bool {
		status, raw := ep.GetJSON(t,
			"/v1/instances/"+instanceID+"/messages?sender_kind=publisher", "")
		if status != http.StatusOK {
			return false
		}
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
		if err := json.Unmarshal(raw, &resp); err != nil {
			return false
		}
		for _, m := range resp.Messages {
			if m.SenderKind != "publisher" || !m.ReceivedAt.After(restartAt) {
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
			if m.DeliveredAt != nil {
				return true
			}
		}
		return false
	})
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

func requireSensorCronAdvancedWatermark(t *testing.T, ctx context.Context, pool *pgxpool.Pool, subID string, original time.Time) {
	t.Helper()
	awaited.Until(t, fmt.Sprintf("sensor-cron to advance next_fire_at past %s, the durable watermark move that "+
		"keeps the next Tick from re-firing the same window", original.UTC().Format(time.RFC3339Nano)), func() bool {
		var nextFire time.Time
		err := pool.QueryRow(ctx,
			`SELECT next_fire_at FROM sensor_cron_state WHERE publisher_subscription_id = $1`, subID,
		).Scan(&nextFire)
		return err == nil && nextFire.After(original)
	})
}

func deployCronSensorTemplate(t *testing.T, ep harness.RimskyEndpoint) string {
	t.Helper()
	cronCfg := map[string]any{"cron": "* * * * *"}
	cronBytes, err := json.Marshal(cronCfg)
	if err != nil {
		t.Fatalf("marshal cron config: %v", err)
	}
	return deployScenarioTemplate(t, ep, map[string]any{
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
					"type":     cronReactorNode,
					"executor": "stub",
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
	})
}

func createCronSensorInstance(t *testing.T, ep harness.RimskyEndpoint, templateID, instanceKey string) string {
	t.Helper()
	return createScenarioInstanceNoWake(t, ep, templateID, instanceKey)
}
