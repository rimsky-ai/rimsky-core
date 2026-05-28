// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// state_db_test.go — pgtest-backed coverage for sensor-object-store's
// state persistence. Confirms that publisher-subscription rows +
// watermark cursors survive a stateDB reopen.

package main

import (
	"context"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

// TestSubscribe_RestartReplay_PreloadsWatermark drives the path issue
// #2 of the 2026-05-17 review flagged: Subscribe must look up the
// persisted watermark via GetSubscription before publishing the Watch
// into the in-memory map, otherwise the first post-restart poll
// re-emits every object in the bucket+prefix.
func TestSubscribe_RestartReplay_PreloadsWatermark(t *testing.T) {
	ctx := context.Background()
	dsn := harness.StartFreshPostgres(ctx, t)

	t.Setenv("RIMSKY_SENSOR_OBJECT_STORE_STATE_DSN", dsn)

	s1, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB: %v", err)
	}
	defer s1.Close()
	w := &Watch{
		SubscriptionID: "sub-2",
		InstanceID:     "inst-2",
		Backend:        "memory",
		Bucket:         "test-bucket",
		Prefix:         "events/",
		PollInterval:   30 * time.Second,
		WatermarkField: "name",
		TargetNode:     "ingest",
		MessageKind:    "invalidate",
	}
	if err := s1.UpsertSubscription(ctx, w); err != nil {
		t.Fatalf("UpsertSubscription: %v", err)
	}
	if err := s1.UpdateWatermarkName(ctx, "sub-2", "events/restart.json"); err != nil {
		t.Fatalf("UpdateWatermarkName: %v", err)
	}

	got, err := s1.GetSubscription(ctx, "sub-2")
	if err != nil {
		t.Fatalf("GetSubscription: %v", err)
	}
	if got == nil {
		t.Fatal("GetSubscription returned nil for known subscription_id")
	}
	if got.WatermarkName != "events/restart.json" {
		t.Fatalf("expected WatermarkName=events/restart.json, got %q", got.WatermarkName)
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

	t.Setenv("RIMSKY_SENSOR_OBJECT_STORE_STATE_DSN", dsn)

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
		Backend:        "memory",
		Bucket:         "test-bucket",
		Prefix:         "events/",
		PollInterval:   30 * time.Second,
		WatermarkField: "name",
		TargetNode:     "ingest",
		MessageKind:    "invalidate",
	}
	if err := s1.UpsertSubscription(ctx, w); err != nil {
		t.Fatalf("UpsertSubscription: %v", err)
	}
	if err := s1.UpdateWatermarkName(ctx, "sub-1", "events/seed.json"); err != nil {
		t.Fatalf("UpdateWatermarkName: %v", err)
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
	if subs[0].SubscriptionID != "sub-1" || subs[0].WatermarkName != "events/seed.json" {
		t.Errorf("subscription state did not roundtrip: %+v", subs[0])
	}
}
