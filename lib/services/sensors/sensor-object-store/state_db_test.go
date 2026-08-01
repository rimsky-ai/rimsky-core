// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package main

import (
	"context"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

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

		MessageType: "invalidate",
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

func TestTouchLastPoll_UpdatesEvenWithoutWatermarkAdvance(t *testing.T) {
	ctx := context.Background()
	dsn := harness.StartFreshPostgres(ctx, t)
	t.Setenv("RIMSKY_SENSOR_OBJECT_STORE_STATE_DSN", dsn)

	s1, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB: %v", err)
	}
	defer s1.Close()
	w := &Watch{
		SubscriptionID: "sub-touch",
		InstanceID:     "inst-touch",
		Backend:        "memory",
		Bucket:         "test-bucket",
		Prefix:         "events/",
		PollInterval:   30 * time.Second,
		WatermarkField: "name",
		MessageType:    "invalidate",
	}
	if err := s1.UpsertSubscription(ctx, w); err != nil {
		t.Fatalf("UpsertSubscription: %v", err)
	}

	before, err := s1.GetSubscription(ctx, "sub-touch")
	if err != nil {
		t.Fatalf("GetSubscription (before): %v", err)
	}
	if before == nil {
		t.Fatal("GetSubscription returned nil")
	}

	touchAt := time.Now().UTC().Add(time.Hour)
	if err := s1.TouchLastPoll(ctx, "sub-touch", touchAt); err != nil {
		t.Fatalf("TouchLastPoll: %v", err)
	}

	var lastPollAt time.Time
	if err := s1.db.QueryRowContext(ctx,
		`SELECT last_poll_at FROM sensor_object_store_state WHERE publisher_subscription_id = $1`,
		"sub-touch").Scan(&lastPollAt); err != nil {
		t.Fatalf("query last_poll_at: %v", err)
	}
	if !lastPollAt.Equal(touchAt) {
		t.Fatalf("last_poll_at = %s, want %s — TouchLastPoll must persist a poll timestamp "+
			"even when no new object was found (a restart-recovery test anchors on this to "+
			"avoid a vacuous stability-window check)", lastPollAt, touchAt)
	}
}

func TestSeenNames_PersistAndRoundTripAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dsn := harness.StartFreshPostgres(ctx, t)
	t.Setenv("RIMSKY_SENSOR_OBJECT_STORE_STATE_DSN", dsn)

	s1, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB: %v", err)
	}
	w := &Watch{
		SubscriptionID: "sub-seen",
		InstanceID:     "inst-seen",
		Backend:        "memory",
		Bucket:         "test-bucket",
		PollInterval:   30 * time.Second,
		WatermarkField: "name",
		MessageType:    "invalidate",
	}
	if err := s1.UpsertSubscription(ctx, w); err != nil {
		t.Fatalf("UpsertSubscription: %v", err)
	}
	if err := s1.AddSeenName(ctx, "sub-seen", "b.json"); err != nil {
		t.Fatalf("AddSeenName: %v", err)
	}
	if err := s1.AddSeenName(ctx, "sub-seen", "a.json"); err != nil {
		t.Fatalf("AddSeenName: %v", err)
	}
	if err := s1.AddSeenName(ctx, "sub-seen", "a.json"); err != nil {
		t.Fatalf("AddSeenName (duplicate insert must not error): %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB after restart: %v", err)
	}
	defer s2.Close()
	names, err := s2.ListSeenNames(ctx, "sub-seen")
	if err != nil {
		t.Fatalf("ListSeenNames: %v", err)
	}
	got := map[string]bool{}
	for _, n := range names {
		got[n] = true
	}
	if len(got) != 2 || !got["a.json"] || !got["b.json"] {
		t.Fatalf("ListSeenNames after restart = %v, want exactly [a.json b.json]", names)
	}

	if err := s2.DeleteSubscription(ctx, "sub-seen"); err != nil {
		t.Fatalf("DeleteSubscription: %v", err)
	}
	names, err = s2.ListSeenNames(ctx, "sub-seen")
	if err != nil {
		t.Fatalf("ListSeenNames after delete: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("ListSeenNames after DeleteSubscription = %v, want empty (cascade cleanup)", names)
	}
}

func TestAdvanceWatermarkTime_ResetsSeenNamesToNewTie(t *testing.T) {
	ctx := context.Background()
	dsn := harness.StartFreshPostgres(ctx, t)
	t.Setenv("RIMSKY_SENSOR_OBJECT_STORE_STATE_DSN", dsn)

	s1, err := openStateDB(ctx)
	if err != nil {
		t.Fatalf("openStateDB: %v", err)
	}
	defer s1.Close()
	w := &Watch{
		SubscriptionID: "sub-lm",
		InstanceID:     "inst-lm",
		Backend:        "memory",
		Bucket:         "test-bucket",
		PollInterval:   30 * time.Second,
		WatermarkField: "last_modified",
		MessageType:    "invalidate",
	}
	if err := s1.UpsertSubscription(ctx, w); err != nil {
		t.Fatalf("UpsertSubscription: %v", err)
	}
	tied := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s1.AdvanceWatermarkTime(ctx, "sub-lm", tied, "sibling-a.json"); err != nil {
		t.Fatalf("AdvanceWatermarkTime: %v", err)
	}
	if err := s1.AddSeenName(ctx, "sub-lm", "sibling-b.json"); err != nil {
		t.Fatalf("AddSeenName: %v", err)
	}
	names, err := s1.ListSeenNames(ctx, "sub-lm")
	if err != nil {
		t.Fatalf("ListSeenNames: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("ListSeenNames before advance = %v, want 2 tied siblings", names)
	}

	later := tied.Add(time.Hour)
	if err := s1.AdvanceWatermarkTime(ctx, "sub-lm", later, "next.json"); err != nil {
		t.Fatalf("AdvanceWatermarkTime: %v", err)
	}
	names, err = s1.ListSeenNames(ctx, "sub-lm")
	if err != nil {
		t.Fatalf("ListSeenNames: %v", err)
	}
	if len(names) != 1 || names[0] != "next.json" {
		t.Fatalf("ListSeenNames after watermark advanced = %v, want exactly [next.json] (stale tie set pruned)", names)
	}

	got, err := s1.GetSubscription(ctx, "sub-lm")
	if err != nil {
		t.Fatalf("GetSubscription: %v", err)
	}
	if got.WatermarkTime == nil || !got.WatermarkTime.Equal(later) {
		t.Fatalf("WatermarkTime = %v, want %v", got.WatermarkTime, later)
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

		MessageType: "invalidate",
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
