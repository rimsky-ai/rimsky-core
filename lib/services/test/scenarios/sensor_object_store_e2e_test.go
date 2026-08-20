// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
	"github.com/rimsky-ai/rimsky-core/test/support/awaited"
)

const objectStorePublisherName = "watcher"

const objectStoreReactorNode = "reactor"

const objectStoreMessageType = "object/discovered"

const objectStoreBucket = "events"

const objectStorePollIntervalConfig = "1s"

func TestSensorObjectStore_FilesystemBackendRestartWatermark(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	netName := harness.SharedNetworkName(ctx, t)
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

	ep.WaitForSubscriptionsActive(t, instanceID)

	statePool := connectSensorStatePostgres(ctx, t, statePG.hostDSN)
	defer statePool.Close()
	subID := waitForSensorObjectStoreSubscriptionPersisted(t, ctx, statePool)
	t.Logf("sensor-object-store persisted subscription %s", subID)

	objectAName := "001-event.json"
	objectABytes := []byte(`{"event":"created","gen":1,"payload":"first"}`)
	objectAEtag := fnvHexOf(objectABytes)
	sensor.PutObject(ctx, objectStoreBucket, objectAName, objectABytes)

	requirePublisherMessageCountReaches(t, ep, instanceID, 1, "first-object-discovered")
	requirePublisherMessageCountStableWhile(t, ep, instanceID, 1, "exactly-one-after-first-object",
		objectStorePollsAdvanced(t, ctx, statePool, subID, 5))

	requireObjectStoreMessagePayload(t, ep, instanceID, objectStoreObservation{
		Backend:    "filesystem",
		Bucket:     objectStoreBucket,
		Prefix:     "",
		ObjectName: objectAName,
		Size:       int64(len(objectABytes)),
		ETag:       objectAEtag,
	}, "first-object-payload")

	originalWatermark := waitForSensorObjectStoreWatermark(t, ctx, statePool, subID)
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
	waitForSensorObjectStorePollSince(t, ctx, statePool, subID, restartedAt)
	requirePublisherMessageCountStableWhile(t, ep, instanceID, preRestartCount,
		"watermark-suppressed-re-emit-after-restart",
		objectStorePollsAdvanced(t, ctx, statePool, subID, 5))

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

	requirePublisherMessageCountReaches(t, ep, instanceID, preRestartCount+1, "second-object-after-restart")
	requirePublisherMessageCountStableWhile(t, ep, instanceID, preRestartCount+1,
		"exactly-one-after-second-object",
		objectStorePollsAdvanced(t, ctx, statePool, subID, 5))

	requireObjectStoreMessagePayload(t, ep, instanceID, objectStoreObservation{
		Backend:    "filesystem",
		Bucket:     objectStoreBucket,
		Prefix:     "",
		ObjectName: objectBName,
		Size:       int64(len(objectBBytes)),
		ETag:       objectBEtag,
	}, "second-object-payload")

	requireSensorObjectStoreWatermarkAdvanced(t, ctx, statePool, subID, objectAName, objectBName)
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
	awaited.Until(t, fmt.Sprintf("a publisher message on instance %s carrying the observed object metadata %+v (%s)",
		instanceID, want, label), func() bool {
		status, raw := ep.GetJSON(t,
			"/v1/instances/"+instanceID+"/messages?sender_kind=publisher", "")
		if status != http.StatusOK {
			return false
		}
		var resp struct {
			Messages []struct {
				Type       string          `json:"type"`
				Sender     string          `json:"sender"`
				SenderKind string          `json:"sender_kind"`
				Payload    json.RawMessage `json:"payload"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return false
		}
		for _, m := range resp.Messages {
			if m.SenderKind != "publisher" {
				continue
			}
			var got objectStoreObservation
			var payloadAny map[string]any
			if err := json.Unmarshal(m.Payload, &payloadAny); err != nil {
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
			if got == want {
				return true
			}
		}
		return false
	})
}

func waitForSensorObjectStorePollSince(t *testing.T, ctx context.Context, pool *pgxpool.Pool, subID string, since time.Time) {
	t.Helper()
	awaited.Until(t, fmt.Sprintf("sensor-object-store to record a poll on subscription %s after %s, so the "+
		"stability check that follows cannot pass before the recovered watch's first poll runs",
		subID, since.Format(time.RFC3339Nano)), func() bool {
		pollAt := readSensorObjectStoreLastPollAt(t, ctx, pool, subID)
		return pollAt.Valid && pollAt.Time.After(since)
	})
}

func readSensorObjectStoreLastPollAt(t *testing.T, ctx context.Context, pool *pgxpool.Pool, subID string) sql.NullTime {
	t.Helper()
	var pollAt sql.NullTime
	if err := pool.QueryRow(ctx,
		`SELECT last_poll_at FROM sensor_object_store_state WHERE publisher_subscription_id = $1`,
		subID,
	).Scan(&pollAt); err != nil {
		return sql.NullTime{}
	}
	return pollAt
}

func objectStorePollsAdvanced(t *testing.T, ctx context.Context, pool *pgxpool.Pool, subID string, polls int) sensorProgress {
	t.Helper()
	last := readSensorObjectStoreLastPollAt(t, ctx, pool, subID)
	advances := 0
	return sensorProgress{
		description: fmt.Sprintf("sensor-object-store to record %d more poll(s) on subscription %s", polls, subID),
		reached: func() bool {
			cur := readSensorObjectStoreLastPollAt(t, ctx, pool, subID)
			if cur.Valid && (!last.Valid || cur.Time.After(last.Time)) {
				advances++
				last = cur
			}
			return advances >= polls
		},
	}
}

func waitForSensorObjectStoreSubscriptionPersisted(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	var subID string
	awaited.Until(t, "the Subscribe path to write a subscription through to sensor_object_store_state, "+
		"without which the restart-recovery proof is unobservable", func() bool {
		return pool.QueryRow(ctx,
			`SELECT publisher_subscription_id FROM sensor_object_store_state LIMIT 1`,
		).Scan(&subID) == nil
	})
	return subID
}

func waitForSensorObjectStoreWatermark(t *testing.T, ctx context.Context, pool *pgxpool.Pool, subID string) string {
	t.Helper()
	var watermark string
	awaited.Until(t, fmt.Sprintf("the emit path to write a non-empty watermark_name for subscription %s "+
		"through to durable state, without which the restart-recovery proof is unobservable", subID), func() bool {
		var wm string
		if err := pool.QueryRow(ctx,
			`SELECT COALESCE(watermark_name, '') FROM sensor_object_store_state WHERE publisher_subscription_id = $1`,
			subID,
		).Scan(&wm); err != nil || wm == "" {
			return false
		}
		watermark = wm
		return true
	})
	return watermark
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

func requireSensorObjectStoreWatermarkAdvanced(t *testing.T, ctx context.Context, pool *pgxpool.Pool, subID, previous, expected string) {
	t.Helper()
	awaited.Until(t, fmt.Sprintf("sensor-object-store to advance watermark_name on subscription %s from %q to %q, "+
		"the durable cursor move that keeps the next poll on the same bucket from re-emitting",
		subID, previous, expected), func() bool {
		var wm string
		err := pool.QueryRow(ctx,
			`SELECT COALESCE(watermark_name, '') FROM sensor_object_store_state WHERE publisher_subscription_id = $1`,
			subID,
		).Scan(&wm)
		return err == nil && wm == expected
	})
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
