// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

func TestSubscribe_RestartReplay_PreloadsLastHash(t *testing.T) {
	ctx := context.Background()
	dsn := harness.StartFreshPostgres(ctx, t)

	t.Setenv("RIMSKY_SENSOR_HTTP_STATE_DSN", dsn)

	s1, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB: %v", err)
	}
	defer s1.Close()
	w := &Watch{
		SubscriptionID: "sub-2",
		InstanceID:     "inst-2",
		URL:            "http://example.test/feed",
		PollInterval:   30 * time.Second,
		MatchStatus:    []int{200},

		MessageType: "invalidate",
	}
	if err := s1.UpsertSubscription(ctx, w); err != nil {
		t.Fatalf("UpsertSubscription: %v", err)
	}
	if err := s1.UpdateLastHash(ctx, "sub-2", "sha256-restart"); err != nil {
		t.Fatalf("UpdateLastHash: %v", err)
	}

	got, err := s1.GetSubscription(ctx, "sub-2")
	if err != nil {
		t.Fatalf("GetSubscription: %v", err)
	}
	if got == nil {
		t.Fatal("GetSubscription returned nil for known subscription_id")
	}
	if got.LastHash != "sha256-restart" {
		t.Fatalf("expected LastHash=sha256-restart, got %q", got.LastHash)
	}

	got, err = s1.GetSubscription(ctx, "sub-nonexistent")
	if err != nil {
		t.Fatalf("GetSubscription nonexistent: %v", err)
	}
	if got != nil {
		t.Fatal("GetSubscription should return nil for unknown id")
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

func TestStateDB_PersistsAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dsn := harness.StartFreshPostgres(ctx, t)

	t.Setenv("RIMSKY_SENSOR_HTTP_STATE_DSN", dsn)

	s1, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB: %v", err)
	}
	if s1 == nil {
		t.Fatal("openStateDB returned nil with DSN set")
	}

	w := &Watch{
		SubscriptionID: "sub-1",
		InstanceID:     "inst-1",
		URL:            "http://example.test/feed",
		PollInterval:   30 * time.Second,
		MatchStatus:    []int{200},
		MatchJSONKey:   "status",
		MatchJSONVal:   "ready",

		MessageType: "invalidate",
	}
	if err := s1.UpsertSubscription(ctx, w); err != nil {
		t.Fatalf("UpsertSubscription: %v", err)
	}
	if err := s1.UpdateLastHash(ctx, "sub-1", "sha256-abc"); err != nil {
		t.Fatalf("UpdateLastHash: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB after restart: %v", err)
	}
	defer s2.Close()
	subs, err := s2.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("expected 1 subscription, got %d", len(subs))
	}
	if subs[0].SubscriptionID != "sub-1" || subs[0].LastHash != "sha256-abc" {
		t.Errorf("subscription state did not roundtrip: %+v", subs[0])
	}
	if subs[0].LastPollAt.IsZero() {
		t.Errorf("subscription state did not restore last_poll_at: %+v", subs[0])
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

	w := &Watch{
		SubscriptionID: "sub-never-polled",
		InstanceID:     "inst-1",
		URL:            "http://example.test/feed",
		PollInterval:   30 * time.Second,
		MessageType:    "invalidate",
	}
	if err := s1.UpsertSubscription(ctx, w); err != nil {
		t.Fatalf("UpsertSubscription: %v", err)
	}

	got, err := s1.GetSubscription(ctx, "sub-never-polled")
	if err != nil {
		t.Fatalf("GetSubscription: %v", err)
	}
	if got == nil {
		t.Fatal("GetSubscription returned nil for known subscription_id")
	}
	if !got.LastPollAt.IsZero() {
		t.Fatalf("expected zero LastPollAt for a subscription never polled, got %v", got.LastPollAt)
	}
}

func TestAttachStateDB_RestoresLastPollAtSoRestartDoesNotForceImmediateRepoll(t *testing.T) {
	ctx := context.Background()
	dsn := harness.StartFreshPostgres(ctx, t)

	t.Setenv("RIMSKY_SENSOR_HTTP_STATE_DSN", dsn)

	s1, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB: %v", err)
	}
	w := &Watch{
		SubscriptionID: "sub-attach",
		InstanceID:     "inst-1",
		URL:            "http://example.test/feed",
		PollInterval:   time.Hour,
		MessageType:    "invalidate",
	}
	if err := s1.UpsertSubscription(ctx, w); err != nil {
		t.Fatalf("UpsertSubscription: %v", err)
	}
	if err := s1.UpdateLastHash(ctx, "sub-attach", "sha256-attach"); err != nil {
		t.Fatalf("UpdateLastHash: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB after restart: %v", err)
	}
	defer s2.Close()

	svc := NewSensorService("", loopbackGuard(t), noopLogger{})
	svc.AttachStateDB(s2)

	restored, ok := svc.watches["sub-attach"]
	if !ok {
		t.Fatal("AttachStateDB did not restore the subscription")
	}
	if restored.LastPollAt.IsZero() {
		t.Fatal("AttachStateDB left LastPollAt zero after restart — every restored watch would immediately re-poll")
	}
	if restored.LastPollAt.Before(restored.StartedAt) {
		t.Fatalf("restored LastPollAt = %v, before the row's own start stamp %v — the restored watch would claim a poll that never happened",
			restored.LastPollAt, restored.StartedAt)
	}
}
