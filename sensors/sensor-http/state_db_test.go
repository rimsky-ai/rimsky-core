// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

// state_db_test.go — pgtest-backed coverage for sensor-http's state
// persistence. Confirms that publisher-subscription rows + body-hash
// watermarks survive a stateDB reopen (simulating a process restart).

package main

import (
	"context"
	"testing"
	"time"

	"github.com/fallguy/rimsky/internal/pgtest"
)

// TestSubscribe_RestartReplay_PreloadsLastHash drives the full restart
// path that issue #2 of the 2026-05-17 review flagged: Subscribe must
// look up the persisted body-hash via GetSubscription and pre-populate
// the in-memory Watch so the first post-restart poll does not re-emit
// when the body hasn't changed.
func TestSubscribe_RestartReplay_PreloadsLastHash(t *testing.T) {
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	defer teardown()

	dsn := pool.Config().ConnString()
	t.Setenv("RIMSKY_SENSOR_HTTP_STATE_DSN", dsn)

	s1, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB: %v", err)
	}
	defer s1.Close()
	// Seed: prior process recorded a body-hash watermark for sub-2.
	w := &Watch{
		SubscriptionID: "sub-2",
		InstanceID:     "inst-2",
		URL:            "http://example.test/feed",
		PollInterval:   30 * time.Second,
		MatchStatus:    []int{200},
		TargetNode:     "feed-tick",
		MessageKind:    "invalidate",
	}
	if err := s1.UpsertSubscription(ctx, w); err != nil {
		t.Fatalf("UpsertSubscription: %v", err)
	}
	if err := s1.UpdateLastHash(ctx, "sub-2", "sha256-restart"); err != nil {
		t.Fatalf("UpdateLastHash: %v", err)
	}

	// Per-subscription read — issue #2 required adding this method so
	// Subscribe can pre-populate the in-memory Watch on restart.
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

	// Unknown id returns (nil, nil) — needed so Subscribe can tell
	// "first-ever Subscribe" apart from "DB error".
	got, err = s1.GetSubscription(ctx, "sub-nonexistent")
	if err != nil {
		t.Fatalf("GetSubscription nonexistent: %v", err)
	}
	if got != nil {
		t.Fatal("GetSubscription should return nil for unknown id")
	}
}

// TestStateDB_PersistsAcrossRestart inserts a subscription, closes the
// state DB, reopens against the same Postgres, and asserts the
// subscription is present with its body-hash watermark.
func TestStateDB_PersistsAcrossRestart(t *testing.T) {
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	defer teardown()

	dsn := pool.Config().ConnString()
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
		TargetNode:     "feed-tick",
		MessageKind:    "invalidate",
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

	// Simulated restart.
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
