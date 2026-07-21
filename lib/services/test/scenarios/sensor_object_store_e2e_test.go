// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: sensor
// @concept: publisher
// @concept: publisher-subscription
// @story: sensor-object-store
package scenarios

import (
	"context"
	"database/sql"
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

const objectStorePublisherName = "watcher"

const objectStoreReactorNode = "reactor"

const objectStoreMessageType = "object/discovered"

const objectStoreBucket = "events"

const objectStorePollIntervalConfig = "1s"
const objectStorePollInterval = 1 * time.Second

func TestSensorObjectStore_FilesystemBackendRestartWatermark(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.NewNetwork(ctx, t)
	statePG := startSensorStatePostgres(ctx, t, netName, "sensor-object-store-pg")

	rimskyAlias := harness.NextRimskyAlias()
	rimskyInternalURL := fmt.Sprintf("http://%s:8080", rimskyAlias)
	sensor := harness.StartSensorObjectStoreHandle(ctx, t, netName, "sensor-object-store", rimskyInternalURL, statePG.internalDSN)

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithExistingNetwork(netName),
		harness.WithRimskyAlias(rimskyAlias),
		harness.WithPublisher(objectStorePublisherName, sensor.Endpoint),
	)

	templateID := deployObjectStoreSensorTemplate(t, ep)
	instanceID := createObjectStoreSensorInstance(t, ep, templateID, "ck-sensor-object-store-e2e")

	ep.WaitForSubscriptionsActive(t, instanceID, 90*time.Second)

	statePool := connectSensorStatePostgres(ctx, t, statePG.hostDSN)
	defer statePool.Close()
	subID := waitForSensorObjectStoreSubscriptionPersisted(t, ctx, statePool, 60*time.Second)
	t.Logf("sensor-object-store persisted subscription %s", subID)

	objectAName := "001-event.json"
	objectABytes := []byte(`{"event":"created","gen":1,"payload":"first"}`)
	objectAEtag := fnvHexOf(objectABytes)
	sensor.PutObject(ctx, objectStoreBucket, objectAName, objectABytes)

	requirePublisherMessageCountReaches(t, ep, instanceID, 1,
		20*time.Second, "first-object-discovered")
	requirePublisherMessageCountStable(t, ep, instanceID, 1,
		5*objectStorePollInterval, "exactly-one-after-first-object")

	requireObjectStoreMessagePayload(t, ep, instanceID, objectStoreObservation{
		Backend:    "filesystem",
		Bucket:     objectStoreBucket,
		Prefix:     "",
		ObjectName: objectAName,
		Size:       int64(len(objectABytes)),
		ETag:       objectAEtag,
	}, "first-object-payload")

	originalWatermark := waitForSensorObjectStoreWatermark(t, ctx, statePool, subID, 20*time.Second)
	if originalWatermark != objectAName {
		t.Fatalf("sensor-object-store watermark = %q, want %q after first emit — "+
			"the persisted cursor must equal the most-recently-emitted object name "+
			"or the restart-replay gate is unobservable", originalWatermark, objectAName)
	}

	preRestartCount := publisherMessageCount(t, ep, instanceID)
	sensor.Stop(ctx)
	t.Logf("sensor-object-store stopped; pre-restart message count=%d", preRestartCount)

	restartedAt := time.Now().UTC()
	sensor.Restart(ctx)
	t.Logf("sensor-object-store restarted; recovered watermark must suppress re-emit " +
		"when object-A is re-dropped into the fresh container's bucket")

	sensor.PutObject(ctx, objectStoreBucket, objectAName, objectABytes)
	waitForSensorObjectStorePollSince(t, ctx, statePool, subID, restartedAt, 30*time.Second)
	requirePublisherMessageCountStable(t, ep, instanceID, preRestartCount,
		5*objectStorePollInterval, "watermark-suppressed-re-emit-after-restart")

	postRestartWatermark := readSensorObjectStoreWatermark(t, ctx, statePool, subID)
	if postRestartWatermark != objectAName {
		t.Fatalf("sensor-object-store watermark = %q after restart, want %q — "+
			"the cursor must be unchanged when no new objects appear (a regression "+
			"would indicate the recovered Watch lost its cursor)",
			postRestartWatermark, objectAName)
	}

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

	requireSensorObjectStoreWatermarkAdvanced(t, ctx, statePool, subID, objectAName, objectBName, 20*time.Second)
}

type objectStoreObservation struct {
	Backend    string
	Bucket     string
	Prefix     string
	ObjectName string
	Size       int64
	ETag       string
}

func fnvHexOf(b []byte) string {
	h := fnv.New64a()
	_, _ = h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}

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
					Type       string          `json:"type"`
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

func waitForSensorObjectStorePollSince(t *testing.T, ctx context.Context, pool *pgxpool.Pool, subID string, since time.Time, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	var lastSeen sql.NullTime
	for time.Now().Before(end) {
		var pollAt sql.NullTime
		err := pool.QueryRow(ctx,
			`SELECT last_poll_at FROM sensor_object_store_state WHERE publisher_subscription_id = $1`,
			subID,
		).Scan(&pollAt)
		if err == nil {
			lastSeen = pollAt
			if pollAt.Valid && pollAt.Time.After(since) {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("sensor-object-store never recorded a poll after restart (since %s) within %v "+
		"(last last_poll_at=%v) — without anchoring on an observed post-restart poll, the "+
		"stability-window check that follows could start and finish before the recovered "+
		"watch's first poll ever runs, passing vacuously",
		since.Format(time.RFC3339Nano), deadline, lastSeen)
}

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
	return deployScenarioTemplate(t, ep, map[string]any{
		"spec": map[string]any{
			"name":    "sensor-object-store-e2e",
			"version": "1",
			"messages": []map[string]any{
				{
					"type": objectStoreMessageType,
					"body_schema": map[string]any{
						"type":                 "object",
						"properties":           map[string]any{},
						"additionalProperties": true,
					},
				},
			},
			"nodes": []map[string]any{
				{
					"type": objectStoreReactorNode,
					"subscribes": []map[string]any{
						{
							"node":                   objectStoreMessageType,
							"type":                   "terminal/success",
							"force_upstream_refresh": false,
						},
					},
				},
			},
			"publishers": []map[string]any{
				{
					"name":         objectStorePublisherName,
					"kind":         "object-store",
					"config":       json.RawMessage(configBytes),
					"message_type": objectStoreMessageType,
				},
			},
		},
	})
}

func createObjectStoreSensorInstance(t *testing.T, ep harness.RimskyEndpoint, templateID, instanceKey string) string {
	t.Helper()
	return createScenarioInstanceNoWake(t, ep, templateID, instanceKey)
}
