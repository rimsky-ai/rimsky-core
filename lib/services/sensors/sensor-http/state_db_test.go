// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"context"
	"testing"
	"time"

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
}
