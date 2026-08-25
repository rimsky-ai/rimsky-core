// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

const upstreamSecret = "s3cret-upstream-token"

func subscribeRequestWithAuth(t *testing.T, subscriptionID string) *genv1.SubscribeRequest {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"url":           "http://example.test/feed",
		"poll_interval": "30s",
		"auth": map[string]any{
			"mode":   "secret_header",
			"header": "X-Upstream-Token",
			"secret": upstreamSecret,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &genv1.SubscribeRequest{
		PublisherSubscriptionId: subscriptionID,
		InstanceId:              "inst-1",
		Kind:                    "http",
		MessageType:             "invalidate",
		ResolvedConfig:          raw,
	}
}

func TestSubscribe_RestartReplay_PreloadsLastHash(t *testing.T) {
	ctx := context.Background()
	dsn := harness.StartFreshPostgres(ctx, t)

	t.Setenv("RIMSKY_SENSOR_HTTP_STATE_DSN", dsn)

	s1, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB: %v", err)
	}
	defer s1.Close()
	if err := s1.UpsertSubscription(ctx, &Watch{SubscriptionID: "sub-2"}); err != nil {
		t.Fatalf("UpsertSubscription: %v", err)
	}
	if err := s1.UpdateLastHash(ctx, "sub-2", "sha256-restart"); err != nil {
		t.Fatalf("UpdateLastHash: %v", err)
	}

	got, err := s1.GetWatermark(ctx, "sub-2")
	if err != nil {
		t.Fatalf("GetWatermark: %v", err)
	}
	if got == nil {
		t.Fatal("GetWatermark returned nil for known subscription_id")
	}
	if got.LastHash != "sha256-restart" {
		t.Fatalf("expected LastHash=sha256-restart, got %q", got.LastHash)
	}

	got, err = s1.GetWatermark(ctx, "sub-nonexistent")
	if err != nil {
		t.Fatalf("GetWatermark nonexistent: %v", err)
	}
	if got != nil {
		t.Fatal("GetWatermark should return nil for unknown id")
	}
}

func TestSubscribe_StateUpsertFailure_FailsRPCAndRollsBackInMemoryWatch(t *testing.T) {
	ctx := context.Background()
	dsn := harness.StartFreshPostgres(ctx, t)
	t.Setenv("RIMSKY_SENSOR_HTTP_STATE_DSN", dsn)

	state, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB: %v", err)
	}
	if err := state.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s := NewSensorService("", loopbackGuard(t), noopLogger{})
	s.state = state
	raw, _ := json.Marshal(map[string]any{"url": "http://example.test/feed", "poll_interval": "10s"})
	_, err = s.Subscribe(ctx, &genv1.SubscribeRequest{
		PublisherSubscriptionId: "sub-1", InstanceId: "inst-1", Kind: "http", ResolvedConfig: raw,
	})
	if err == nil {
		t.Fatal("expected Subscribe to fail when the state DB upsert fails, " +
			"so the caller (publisher-lifecycle) retries instead of believing the subscription durable")
	}
	if _, exists := s.watches["sub-1"]; exists {
		t.Error("a failed persist must not leave an in-memory watch behind")
	}
}

// @decision: secret-at-rest-posture
func TestStateDB_KeepsNoCopyOfTheUpstreamCredential(t *testing.T) {
	ctx := context.Background()
	dsn := harness.StartFreshPostgres(ctx, t)
	t.Setenv("RIMSKY_SENSOR_HTTP_STATE_DSN", dsn)

	state, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB: %v", err)
	}
	defer state.Close()

	s := NewSensorService("", loopbackGuard(t), noopLogger{})
	s.AttachStateDB(state)
	if _, err := s.Subscribe(ctx, subscribeRequestWithAuth(t, "sub-auth")); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if s.watches["sub-auth"].Auth == nil {
		t.Fatal("the mounted watch carries no auth block; the poll would send no credentials")
	}

	rows, err := state.db.Query(ctx, `SELECT to_jsonb(t)::text FROM sensor_http_state t`)
	if err != nil {
		t.Fatalf("read the sensor's own state: %v", err)
	}
	defer rows.Close()
	persisted := 0
	for rows.Next() {
		var dumped string
		if err := rows.Scan(&dumped); err != nil {
			t.Fatalf("scan row: %v", err)
		}
		persisted++
		if strings.Contains(dumped, upstreamSecret) {
			t.Errorf("the sensor's own state holds the upstream credential in clear: %s", dumped)
		}
	}
	if persisted != 1 {
		t.Fatalf("expected one persisted row for the mounted subscription, got %d", persisted)
	}
}

// @decision: secret-at-rest-posture
func TestRestartTakesTheCredentialBackFromResyncNotFromItsOwnState(t *testing.T) {
	ctx := context.Background()
	dsn := harness.StartFreshPostgres(ctx, t)
	t.Setenv("RIMSKY_SENSOR_HTTP_STATE_DSN", dsn)

	first, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB: %v", err)
	}
	before := NewSensorService("", loopbackGuard(t), noopLogger{})
	before.AttachStateDB(first)
	if _, err := before.Subscribe(ctx, subscribeRequestWithAuth(t, "sub-auth")); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if err := first.UpdateLastHash(ctx, "sub-auth", "sha256-before-restart"); err != nil {
		t.Fatalf("UpdateLastHash: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB after restart: %v", err)
	}
	defer second.Close()
	after := NewSensorService("", loopbackGuard(t), noopLogger{})
	after.AttachStateDB(second)

	if _, mounted := after.watches["sub-auth"]; mounted {
		t.Fatal("the restarted sensor mounted a watch from its own state; it would poll before resync " +
			"delivered the credentials, and report the subscription live so resync never arrives")
	}

	if _, err := after.Subscribe(ctx, subscribeRequestWithAuth(t, "sub-auth")); err != nil {
		t.Fatalf("resync Subscribe: %v", err)
	}
	restored, mounted := after.watches["sub-auth"]
	if !mounted {
		t.Fatal("resync did not mount the subscription")
	}
	if restored.Auth == nil || restored.Auth.Secret != upstreamSecret {
		t.Fatalf("resync did not restore the operator's credentials: %+v", restored.Auth)
	}
	if restored.LastHash != "sha256-before-restart" {
		t.Errorf("the watermark did not survive the restart: LastHash = %q", restored.LastHash)
	}
	if restored.LastPollAt.IsZero() {
		t.Error("the resynced watch left LastPollAt zero after a restart — it would immediately re-poll")
	}
	if restored.LastPollAt.Before(restored.StartedAt) {
		t.Errorf("restored LastPollAt = %v, before the row's own start stamp %v — the watch would claim a poll that never happened",
			restored.LastPollAt, restored.StartedAt)
	}
}

func TestStateDB_LastPollAtZeroWhenNeverPolled(t *testing.T) {
	ctx := context.Background()
	dsn := harness.StartFreshPostgres(ctx, t)

	t.Setenv("RIMSKY_SENSOR_HTTP_STATE_DSN", dsn)

	s1, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB: %v", err)
	}
	defer s1.Close()

	if err := s1.UpsertSubscription(ctx, &Watch{SubscriptionID: "sub-never-polled"}); err != nil {
		t.Fatalf("UpsertSubscription: %v", err)
	}

	got, err := s1.GetWatermark(ctx, "sub-never-polled")
	if err != nil {
		t.Fatalf("GetWatermark: %v", err)
	}
	if got == nil {
		t.Fatal("GetWatermark returned nil for known subscription_id")
	}
	if !got.LastPollAt.IsZero() {
		t.Fatalf("expected zero LastPollAt for a subscription never polled, got %v", got.LastPollAt)
	}
}
