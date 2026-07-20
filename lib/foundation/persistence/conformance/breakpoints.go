// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: breakpoint

package conformance

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func newConformanceBreakpoint(instanceID shared.UUID, customise func(*persistence.BreakpointRow)) persistence.BreakpointRow {
	bp := persistence.BreakpointRow{
		InstanceID:     instanceID,
		Matcher:        map[string]any{"node_type": "x"},
		Checkpoint:     persistence.CheckpointBeforeDispatch,
		Mode:           persistence.BreakpointModeNotifyOnly,
		OverflowPolicy: persistence.OverflowDropOldest,
		HitTTLSeconds:  300,
		CreatedByKey:   "test-key",
		CreatedAt:      time.Now(),
	}
	if customise != nil {
		customise(&bp)
	}
	return bp
}

func makeConformanceHit(bpID, instanceID shared.UUID, customise func(*persistence.BreakpointHitRow)) persistence.BreakpointHitRow {
	h := persistence.BreakpointHitRow{
		BreakpointID: bpID,
		InstanceID:   instanceID,
		Checkpoint:   persistence.CheckpointBeforeDispatch,
		Mode:         persistence.BreakpointModePause,
		Snapshot:     map[string]any{"node_type": "x"},
		HitAt:        time.Now(),
	}
	if customise != nil {
		customise(&h)
	}
	return h
}

func createConformanceBreakpoint(t *testing.T, ctx context.Context, store persistence.Tables, bp persistence.BreakpointRow) shared.UUID {
	t.Helper()
	var id shared.UUID
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		id, err = store.Breakpoints().Create(ctx, bp, tx)
		return err
	}); err != nil {
		t.Fatalf("create breakpoint: %v", err)
	}
	return id
}

func TestBreakpoints(t *testing.T, d persistence.Database) {
	t.Helper()
	ctx := context.Background()
	store := d.Tables()

	t.Run("CreateGetRoundTrip", func(t *testing.T) {
		fix := seedFixtureSet(ctx, t, d)
		signal := "ProducerOpened"
		ttl := 600
		want := newConformanceBreakpoint(fix.InstanceID, func(b *persistence.BreakpointRow) {
			b.Matcher = map[string]any{"node_type": "x", "signal_type": "ProducerOpened"}
			b.Checkpoint = persistence.CheckpointAfterTerminal
			b.SignalType = &signal
			b.Mode = persistence.BreakpointModeNotifyOnly
			b.OverflowPolicy = persistence.OverflowAutoResumeAfterTTL
			b.HitTTLSeconds = 120
			b.TTLSeconds = &ttl
			b.CreatedByKey = "operator-key"
		})
		id := createConformanceBreakpoint(t, ctx, store, want)

		var got *persistence.BreakpointRow
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			got, err = store.Breakpoints().Get(ctx, id, tx)
			return err
		}); err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got == nil {
			t.Fatalf("Get returned nil")
		}
		if got.Checkpoint != persistence.CheckpointAfterTerminal {
			t.Errorf("checkpoint: got %q want after_terminal", got.Checkpoint)
		}
		if got.Mode != persistence.BreakpointModeNotifyOnly {
			t.Errorf("mode: got %q want notify_only", got.Mode)
		}
		if got.OverflowPolicy != persistence.OverflowAutoResumeAfterTTL {
			t.Errorf("overflow_policy: got %q want auto_resume_after_ttl", got.OverflowPolicy)
		}
		if got.HitTTLSeconds != 120 {
			t.Errorf("hit_ttl_seconds: got %d want 120", got.HitTTLSeconds)
		}
		if got.SignalType == nil || *got.SignalType != signal {
			t.Errorf("signal_type: got %v want %q", got.SignalType, signal)
		}
		if got.TTLSeconds == nil || *got.TTLSeconds != ttl {
			t.Errorf("ttl_seconds: got %v want %d", got.TTLSeconds, ttl)
		}
		if got.ExpiresAt == nil {
			t.Errorf("expires_at: got nil; expected materialised value")
		}
		if got.Matcher["signal_type"] != "ProducerOpened" {
			t.Errorf("matcher round-trip: got %v", got.Matcher)
		}
	})

	t.Run("GetNotFound", func(t *testing.T) {
		var got *persistence.BreakpointRow
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			got, err = store.Breakpoints().Get(ctx, uuid.New(), tx)
			return err
		}); err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got != nil {
			t.Errorf("Get: expected nil for missing id, got %+v", got)
		}
	})

	t.Run("ListForInstanceIncludeExpired", func(t *testing.T) {
		fix := seedFixtureSet(ctx, t, d)
		now := time.Now()
		activeTTL := 3600
		expiredTTL := 60
		activeID := createConformanceBreakpoint(t, ctx, store, newConformanceBreakpoint(fix.InstanceID, func(b *persistence.BreakpointRow) {
			b.CreatedAt = now
			b.TTLSeconds = &activeTTL
			b.Matcher = map[string]any{"label": "active"}
		}))
		expiredID := createConformanceBreakpoint(t, ctx, store, newConformanceBreakpoint(fix.InstanceID, func(b *persistence.BreakpointRow) {
			b.CreatedAt = now.Add(-2 * time.Hour)
			b.TTLSeconds = &expiredTTL
			b.Matcher = map[string]any{"label": "expired"}
		}))

		var rows []persistence.BreakpointRow
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			rows, err = store.Breakpoints().ListForInstance(ctx, fix.InstanceID, false, now, tx)
			return err
		}); err != nil {
			t.Fatalf("ListForInstance(false): %v", err)
		}
		if len(rows) != 1 || rows[0].ID != activeID {
			t.Fatalf("includeExpired=false: got %d rows want 1 (id=%v)", len(rows), activeID)
		}

		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			rows, err = store.Breakpoints().ListForInstance(ctx, fix.InstanceID, true, now, tx)
			return err
		}); err != nil {
			t.Fatalf("ListForInstance(true): %v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("includeExpired=true: got %d rows want 2", len(rows))
		}
		_ = expiredID
	})

	t.Run("IncrementDroppedMonotonic", func(t *testing.T) {
		fix := seedFixtureSet(ctx, t, d)
		id := createConformanceBreakpoint(t, ctx, store, newConformanceBreakpoint(fix.InstanceID, nil))

		for i := 0; i < 3; i++ {
			if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
				return store.Breakpoints().IncrementDropped(ctx, id, tx)
			}); err != nil {
				t.Fatalf("IncrementDropped: %v", err)
			}
		}

		var got *persistence.BreakpointRow
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			got, err = store.Breakpoints().Get(ctx, id, tx)
			return err
		}); err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.DroppedCount != 3 {
			t.Errorf("dropped_count: got %d want 3", got.DroppedCount)
		}
	})

	t.Run("SweepExpired", func(t *testing.T) {
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			_, err := store.Breakpoints().SweepExpired(ctx, time.Now().Add(365*24*time.Hour), tx)
			return err
		}); err != nil {
			t.Fatalf("SweepExpired (pre-clean): %v", err)
		}
		fix := seedFixtureSet(ctx, t, d)
		now := time.Now()
		ttl := 600
		liveID := createConformanceBreakpoint(t, ctx, store, newConformanceBreakpoint(fix.InstanceID, func(b *persistence.BreakpointRow) {
			b.CreatedAt = now
			b.TTLSeconds = &ttl
		}))
		deadID := createConformanceBreakpoint(t, ctx, store, newConformanceBreakpoint(fix.InstanceID, func(b *persistence.BreakpointRow) {
			b.CreatedAt = now.Add(-2 * time.Hour)
			b.TTLSeconds = &ttl
		}))

		var n int
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			n, err = store.Breakpoints().SweepExpired(ctx, now, tx)
			return err
		}); err != nil {
			t.Fatalf("SweepExpired: %v", err)
		}
		if n != 1 {
			t.Errorf("SweepExpired rowcount: got %d want 1", n)
		}

		var live, dead *persistence.BreakpointRow
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			live, err = store.Breakpoints().Get(ctx, liveID, tx)
			if err != nil {
				return err
			}
			dead, err = store.Breakpoints().Get(ctx, deadID, tx)
			return err
		}); err != nil {
			t.Fatalf("Get: %v", err)
		}
		if live == nil {
			t.Errorf("live row deleted unexpectedly")
		}
		if dead != nil {
			t.Errorf("dead row still present after sweep")
		}
	})
}

func TestBreakpointHits(t *testing.T, d persistence.Database) {
	t.Helper()
	ctx := context.Background()
	store := d.Tables()

	t.Run("CreateReturnsIDAndMonotonicSeq", func(t *testing.T) {
		fix := seedFixtureSet(ctx, t, d)
		bpID := createConformanceBreakpoint(t, ctx, store, newConformanceBreakpoint(fix.InstanceID, nil))

		var prevSeq int64 = -1
		for i := 0; i < 5; i++ {
			var (
				id  shared.UUID
				seq int64
			)
			if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
				var err error
				id, seq, err = store.BreakpointHits().Create(ctx, makeConformanceHit(bpID, fix.InstanceID, nil), tx)
				return err
			}); err != nil {
				t.Fatalf("Create: %v", err)
			}
			if id == (shared.UUID{}) {
				t.Fatalf("hit %d: zero id", i)
			}
			if seq <= prevSeq {
				t.Errorf("hit %d: seq %d not monotonic after %d", i, seq, prevSeq)
			}
			prevSeq = seq
		}
	})

	t.Run("ListSinceIncludesResumedRows", func(t *testing.T) {
		fix := seedFixtureSet(ctx, t, d)
		bpID := createConformanceBreakpoint(t, ctx, store, newConformanceBreakpoint(fix.InstanceID, nil))

		var hitIDs [4]shared.UUID
		for i := 0; i < 4; i++ {
			if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
				id, _, err := store.BreakpointHits().Create(ctx, makeConformanceHit(bpID, fix.InstanceID, nil), tx)
				if err != nil {
					return err
				}
				hitIDs[i] = id
				return nil
			}); err != nil {
				t.Fatalf("Create %d: %v", i, err)
			}
		}
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			_, err := store.BreakpointHits().Resume(ctx, hitIDs[1], "operator", nil, tx)
			return err
		}); err != nil {
			t.Fatalf("Resume: %v", err)
		}

		var instRows []persistence.BreakpointHitRow
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			instRows, err = store.BreakpointHits().ListSinceForInstance(ctx, fix.InstanceID, 0, 100, tx)
			return err
		}); err != nil {
			t.Fatalf("ListSinceForInstance: %v", err)
		}
		if len(instRows) != 4 {
			t.Fatalf("ListSinceForInstance: got %d rows want 4", len(instRows))
		}
		for i := 1; i < len(instRows); i++ {
			if instRows[i].Seq <= instRows[i-1].Seq {
				t.Errorf("seq not strictly ascending: %d then %d", instRows[i-1].Seq, instRows[i].Seq)
			}
		}
		var found bool
		for _, r := range instRows {
			if r.ID == hitIDs[1] {
				found = true
				if r.ResumedAt == nil {
					t.Errorf("resumed hit returned with nil ResumedAt")
				}
			}
		}
		if !found {
			t.Errorf("resumed hit %v missing from ListSinceForInstance", hitIDs[1])
		}

		var bpRows []persistence.BreakpointHitRow
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			bpRows, err = store.BreakpointHits().ListSinceForBreakpoint(ctx, bpID, 0, 100, tx)
			return err
		}); err != nil {
			t.Fatalf("ListSinceForBreakpoint: %v", err)
		}
		if len(bpRows) != 4 {
			t.Errorf("ListSinceForBreakpoint: got %d rows want 4", len(bpRows))
		}
	})

	t.Run("ResumeSetsFieldsAndIdempotent", func(t *testing.T) {
		fix := seedFixtureSet(ctx, t, d)
		bpID := createConformanceBreakpoint(t, ctx, store, newConformanceBreakpoint(fix.InstanceID, nil))

		var hitID shared.UUID
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			id, _, err := store.BreakpointHits().Create(ctx, makeConformanceHit(bpID, fix.InstanceID, nil), tx)
			hitID = id
			return err
		}); err != nil {
			t.Fatalf("Create: %v", err)
		}

		overlay := map[string]any{"attr_overrides": map[string]any{"v": 1}}
		var firstResumed bool
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			firstResumed, err = store.BreakpointHits().Resume(ctx, hitID, "operator", overlay, tx)
			return err
		}); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if !firstResumed {
			t.Errorf("Resume: got resumed=false want true on first resume")
		}

		var got *persistence.BreakpointHitRow
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			got, err = store.BreakpointHits().Get(ctx, hitID, tx)
			return err
		}); err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.ResumedAt == nil {
			t.Errorf("ResumedAt nil after Resume")
		}
		if got.ResumedByKey == nil || *got.ResumedByKey != "operator" {
			t.Errorf("ResumedByKey: got %v want %q", got.ResumedByKey, "operator")
		}
		if got.ResumeOverlay == nil {
			t.Errorf("ResumeOverlay nil after Resume")
		}
		firstResumeAt := *got.ResumedAt

		var replayResumed bool
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			replayResumed, err = store.BreakpointHits().Resume(ctx, hitID, "different-operator", nil, tx)
			return err
		}); err != nil {
			t.Fatalf("Resume replay: %v", err)
		}
		if replayResumed {
			t.Errorf("Resume replay: got resumed=true want false")
		}
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			got, err = store.BreakpointHits().Get(ctx, hitID, tx)
			return err
		}); err != nil {
			t.Fatalf("Get after replay: %v", err)
		}
		if got.ResumedAt == nil || !got.ResumedAt.Equal(firstResumeAt) {
			t.Errorf("ResumedAt changed on replay: was %v now %v", firstResumeAt, got.ResumedAt)
		}
		if got.ResumedByKey == nil || *got.ResumedByKey != "operator" {
			t.Errorf("ResumedByKey changed on replay: %v", got.ResumedByKey)
		}

		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			_, err := store.BreakpointHits().Resume(ctx, uuid.New(), "operator", nil, tx)
			return err
		}); !errors.Is(err, shared.ErrBreakpointHitNotFound) {
			t.Errorf("Resume(missing): expected ErrBreakpointHitNotFound, got %v", err)
		}
	})

	t.Run("AutoResumeStale", func(t *testing.T) {
		fix := seedFixtureSet(ctx, t, d)
		autoID := createConformanceBreakpoint(t, ctx, store, newConformanceBreakpoint(fix.InstanceID, func(b *persistence.BreakpointRow) {
			b.OverflowPolicy = persistence.OverflowAutoResumeAfterTTL
			b.HitTTLSeconds = 1
		}))
		dropID := createConformanceBreakpoint(t, ctx, store, newConformanceBreakpoint(fix.InstanceID, func(b *persistence.BreakpointRow) {
			b.OverflowPolicy = persistence.OverflowDropOldest
			b.HitTTLSeconds = 1
		}))
		longTTLID := createConformanceBreakpoint(t, ctx, store, newConformanceBreakpoint(fix.InstanceID, func(b *persistence.BreakpointRow) {
			b.OverflowPolicy = persistence.OverflowAutoResumeAfterTTL
			b.HitTTLSeconds = 3600
		}))

		var autoHitID, dropHitID, longTTLHitID shared.UUID
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			ah, _, err := store.BreakpointHits().Create(ctx, makeConformanceHit(autoID, fix.InstanceID, nil), tx)
			if err != nil {
				return err
			}
			dh, _, err := store.BreakpointHits().Create(ctx, makeConformanceHit(dropID, fix.InstanceID, nil), tx)
			if err != nil {
				return err
			}
			lh, _, err := store.BreakpointHits().Create(ctx, makeConformanceHit(longTTLID, fix.InstanceID, nil), tx)
			if err != nil {
				return err
			}
			autoHitID = ah
			dropHitID = dh
			longTTLHitID = lh
			return nil
		}); err != nil {
			t.Fatalf("seed hits: %v", err)
		}

		var n int
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			n, err = store.BreakpointHits().AutoResumeStale(ctx, time.Now().Add(2*time.Second), tx)
			return err
		}); err != nil {
			t.Fatalf("AutoResumeStale: %v", err)
		}
		if n != 1 {
			t.Errorf("AutoResumeStale rowcount: got %d want 1", n)
		}

		var auto, drop, longTTL *persistence.BreakpointHitRow
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			auto, err = store.BreakpointHits().Get(ctx, autoHitID, tx)
			if err != nil {
				return err
			}
			drop, err = store.BreakpointHits().Get(ctx, dropHitID, tx)
			if err != nil {
				return err
			}
			longTTL, err = store.BreakpointHits().Get(ctx, longTTLHitID, tx)
			return err
		}); err != nil {
			t.Fatalf("Get: %v", err)
		}
		if auto.ResumedAt == nil {
			t.Errorf("auto-resume hit not resumed")
		}
		if auto.ResumedByKey == nil || *auto.ResumedByKey != "sweeper" {
			t.Errorf("auto-resume ResumedByKey: got %v want 'sweeper'", auto.ResumedByKey)
		}
		if drop.ResumedAt != nil {
			t.Errorf("drop_oldest hit erroneously resumed")
		}
		if longTTL.ResumedAt != nil {
			t.Errorf("1-hour-TTL hit erroneously auto-resumed before its TTL elapsed")
		}
	})

	t.Run("DropOldest", func(t *testing.T) {
		fix := seedFixtureSet(ctx, t, d)
		bpID := createConformanceBreakpoint(t, ctx, store, newConformanceBreakpoint(fix.InstanceID, nil))

		const total = 150
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			for i := 0; i < total; i++ {
				if _, _, err := store.BreakpointHits().Create(ctx, makeConformanceHit(bpID, fix.InstanceID, nil), tx); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatalf("seed hits: %v", err)
		}

		const keep = 99
		var n int
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			n, err = store.BreakpointHits().DropOldest(ctx, bpID, keep, tx)
			return err
		}); err != nil {
			t.Fatalf("DropOldest: %v", err)
		}
		if n != total-keep {
			t.Errorf("DropOldest rowcount: got %d want %d", n, total-keep)
		}

		var remaining int
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			remaining, err = store.BreakpointHits().UnresumedCount(ctx, bpID, tx)
			return err
		}); err != nil {
			t.Fatalf("UnresumedCount: %v", err)
		}
		if remaining != keep {
			t.Errorf("UnresumedCount after drop: got %d want %d", remaining, keep)
		}

		var rows []persistence.BreakpointHitRow
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			rows, err = store.BreakpointHits().ListSinceForBreakpoint(ctx, bpID, 0, 1000, tx)
			return err
		}); err != nil {
			t.Fatalf("ListSinceForBreakpoint: %v", err)
		}
		if len(rows) != keep {
			t.Errorf("ListSince after drop: got %d rows want %d", len(rows), keep)
		}
	})

	t.Run("UnresumedCount", func(t *testing.T) {
		fix := seedFixtureSet(ctx, t, d)
		bpID := createConformanceBreakpoint(t, ctx, store, newConformanceBreakpoint(fix.InstanceID, nil))

		var ids [5]shared.UUID
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			for i := 0; i < 5; i++ {
				id, _, err := store.BreakpointHits().Create(ctx, makeConformanceHit(bpID, fix.InstanceID, nil), tx)
				if err != nil {
					return err
				}
				ids[i] = id
			}
			return nil
		}); err != nil {
			t.Fatalf("seed hits: %v", err)
		}
		for _, idx := range []int{0, 2} {
			if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
				_, err := store.BreakpointHits().Resume(ctx, ids[idx], "op", nil, tx)
				return err
			}); err != nil {
				t.Fatalf("Resume: %v", err)
			}
		}
		var n int
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			n, err = store.BreakpointHits().UnresumedCount(ctx, bpID, tx)
			return err
		}); err != nil {
			t.Fatalf("UnresumedCount: %v", err)
		}
		if n != 3 {
			t.Errorf("UnresumedCount: got %d want 3", n)
		}
	})

	t.Run("ConcurrentCappedInsertsHoldCap", func(t *testing.T) {
		const queueCap = 100
		const racers = 16

		fix := seedFixtureSet(ctx, t, d)
		bpID := createConformanceBreakpoint(t, ctx, store, newConformanceBreakpoint(fix.InstanceID, func(b *persistence.BreakpointRow) {
			b.OverflowPolicy = persistence.OverflowDropOldest
		}))

		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			for i := 0; i < queueCap-1; i++ {
				if _, _, err := store.BreakpointHits().Create(ctx, makeConformanceHit(bpID, fix.InstanceID, nil), tx); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatalf("seed %d unresumed hits: %v", queueCap-1, err)
		}

		cappedInsert := func() error {
			return store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
				if err := store.Breakpoints().Lock(ctx, bpID, tx); err != nil {
					return err
				}
				n, err := store.BreakpointHits().UnresumedCount(ctx, bpID, tx)
				if err != nil {
					return err
				}
				if n >= queueCap {
					if _, err := store.BreakpointHits().DropOldest(ctx, bpID, queueCap-1, tx); err != nil {
						return err
					}
					if err := store.Breakpoints().IncrementDropped(ctx, bpID, tx); err != nil {
						return err
					}
				}
				_, _, err = store.BreakpointHits().Create(ctx, makeConformanceHit(bpID, fix.InstanceID, nil), tx)
				return err
			})
		}

		start := make(chan struct{})
		var ready, done sync.WaitGroup
		errs := make(chan error, racers)
		ready.Add(racers)
		done.Add(racers)
		for i := 0; i < racers; i++ {
			go func() {
				defer done.Done()
				ready.Done()
				<-start
				if err := cappedInsert(); err != nil {
					errs <- err
				}
			}()
		}
		ready.Wait()
		close(start)
		done.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("capped insert under race: %v", err)
		}

		var got int
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			got, err = store.BreakpointHits().UnresumedCount(ctx, bpID, tx)
			return err
		}); err != nil {
			t.Fatalf("UnresumedCount: %v", err)
		}
		if got > queueCap {
			t.Fatalf("FOR UPDATE/lock failed to serialize evaluators: unresumed=%d exceeds cap %d", got, queueCap)
		}
		if got != queueCap {
			t.Fatalf("drop_oldest should hold the queue at cap: unresumed=%d want %d", got, queueCap)
		}
	})

	t.Run("ConcurrentResumeExactlyOneWins", func(t *testing.T) {
		const racers = 8

		fix := seedFixtureSet(ctx, t, d)
		bpID := createConformanceBreakpoint(t, ctx, store, newConformanceBreakpoint(fix.InstanceID, nil))

		var hitID shared.UUID
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			id, _, err := store.BreakpointHits().Create(ctx, makeConformanceHit(bpID, fix.InstanceID, nil), tx)
			hitID = id
			return err
		}); err != nil {
			t.Fatalf("Create: %v", err)
		}

		start := make(chan struct{})
		var ready, done sync.WaitGroup
		resumed := make([]bool, racers)
		errs := make(chan error, racers)
		ready.Add(racers)
		done.Add(racers)
		for i := 0; i < racers; i++ {
			go func(idx int) {
				defer done.Done()
				ready.Done()
				<-start
				overlay := map[string]any{"racer": idx}
				if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
					var err error
					resumed[idx], err = store.BreakpointHits().Resume(ctx, hitID, "racer", overlay, tx)
					return err
				}); err != nil {
					errs <- err
				}
			}(i)
		}
		ready.Wait()
		close(start)
		done.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("concurrent resume: %v", err)
		}

		winners := 0
		for _, r := range resumed {
			if r {
				winners++
			}
		}
		if winners != 1 {
			t.Fatalf("concurrent resume: got %d winners want exactly 1 (resumed=%v)", winners, resumed)
		}

		var got *persistence.BreakpointHitRow
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			got, err = store.BreakpointHits().Get(ctx, hitID, tx)
			return err
		}); err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.ResumedAt == nil {
			t.Fatalf("hit must be resumed after racing resumes")
		}
	})

	t.Run("SweepOrphanedUnresumed", func(t *testing.T) {
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			_, err := store.BreakpointHits().SweepOrphanedUnresumed(ctx, time.Now().Add(365*24*time.Hour), tx)
			return err
		}); err != nil {
			t.Fatalf("SweepOrphanedUnresumed (pre-clean): %v", err)
		}
		fix := seedFixtureSet(ctx, t, d)
		blockID := createConformanceBreakpoint(t, ctx, store, newConformanceBreakpoint(fix.InstanceID, func(b *persistence.BreakpointRow) {
			b.OverflowPolicy = persistence.OverflowBlockDispatch
		}))
		autoID := createConformanceBreakpoint(t, ctx, store, newConformanceBreakpoint(fix.InstanceID, func(b *persistence.BreakpointRow) {
			b.OverflowPolicy = persistence.OverflowAutoResumeAfterTTL
			b.HitTTLSeconds = 3600
		}))

		var blockHitID, autoHitID, blockResumedID shared.UUID
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			bh, _, err := store.BreakpointHits().Create(ctx, makeConformanceHit(blockID, fix.InstanceID, nil), tx)
			if err != nil {
				return err
			}
			blockHitID = bh
			br, _, err := store.BreakpointHits().Create(ctx, makeConformanceHit(blockID, fix.InstanceID, nil), tx)
			if err != nil {
				return err
			}
			blockResumedID = br
			ah, _, err := store.BreakpointHits().Create(ctx, makeConformanceHit(autoID, fix.InstanceID, nil), tx)
			if err != nil {
				return err
			}
			autoHitID = ah
			_, err = store.BreakpointHits().Resume(ctx, blockResumedID, "op", nil, tx)
			return err
		}); err != nil {
			t.Fatalf("seed hits: %v", err)
		}

		var n int
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			n, err = store.BreakpointHits().SweepOrphanedUnresumed(ctx, time.Now().Add(time.Minute), tx)
			return err
		}); err != nil {
			t.Fatalf("SweepOrphanedUnresumed: %v", err)
		}
		if n != 1 {
			t.Errorf("SweepOrphanedUnresumed rowcount: got %d want 1 (only the unresumed block_dispatch hit)", n)
		}

		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			got, err := store.BreakpointHits().Get(ctx, blockHitID, tx)
			if err != nil {
				return err
			}
			if got != nil {
				t.Errorf("unresumed block_dispatch hit should have been reaped, got row %+v", got)
			}
			return nil
		}); err != nil {
			t.Fatalf("post-sweep Get(blockHitID): %v", err)
		}

		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			resumed, err := store.BreakpointHits().Get(ctx, blockResumedID, tx)
			if err != nil {
				return err
			}
			if resumed == nil || resumed.ResumedAt == nil {
				t.Errorf("resumed block_dispatch hit must not be reaped: got %+v", resumed)
			}
			auto, err := store.BreakpointHits().Get(ctx, autoHitID, tx)
			if err != nil {
				return err
			}
			if auto == nil || auto.ResumedAt != nil {
				t.Errorf("auto_resume hit must remain unresumed and present: got %+v", auto)
			}
			return nil
		}); err != nil {
			t.Fatalf("post-sweep Get(survivors): %v", err)
		}

		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			n, err = store.BreakpointHits().SweepOrphanedUnresumed(ctx, time.Now().Add(-time.Hour), tx)
			return err
		}); err != nil {
			t.Fatalf("SweepOrphanedUnresumed(past): %v", err)
		}
		if n != 0 {
			t.Errorf("SweepOrphanedUnresumed(past) rowcount: got %d want 0", n)
		}
	})

	t.Run("HasUnresumedPauseHitForInstance", func(t *testing.T) {
		fix := seedFixtureSet(ctx, t, d)
		bpID := createConformanceBreakpoint(t, ctx, store, newConformanceBreakpoint(fix.InstanceID, func(b *persistence.BreakpointRow) {
			b.Mode = persistence.BreakpointModePause
			b.OverflowPolicy = persistence.OverflowBlockDispatch
		}))
		nodeRunID := seedConformanceRunForNode(ctx, t, d, fix.NodeID, fix.FrameID)

		hasUnresumedPause := func() bool {
			t.Helper()
			var got bool
			if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
				var err error
				got, err = store.BreakpointHits().HasUnresumedPauseHitForInstance(ctx, fix.InstanceID, tx)
				return err
			}); err != nil {
				t.Fatalf("HasUnresumedPauseHitForInstance: %v", err)
			}
			return got
		}

		if hasUnresumedPause() {
			t.Fatalf("HasUnresumedPauseHitForInstance: got true before any hit exists")
		}

		var hitID shared.UUID
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			id, _, err := store.BreakpointHits().Create(ctx, persistence.BreakpointHitRow{
				BreakpointID: bpID,
				InstanceID:   fix.InstanceID,
				NodeRunID:    &nodeRunID,
				FrameID:      &fix.FrameID,
				Checkpoint:   persistence.CheckpointBeforeDispatch,
				Mode:         persistence.BreakpointModePause,
				Snapshot:     map[string]any{"node_type": "x"},
			}, tx)
			hitID = id
			return err
		}); err != nil {
			t.Fatalf("Create pause hit: %v", err)
		}

		if !hasUnresumedPause() {
			t.Fatalf("HasUnresumedPauseHitForInstance: got false with an unresumed pause hit bound to an in-flight run")
		}

		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			_, err := store.BreakpointHits().Resume(ctx, hitID, "operator", nil, tx)
			return err
		}); err != nil {
			t.Fatalf("Resume: %v", err)
		}

		if hasUnresumedPause() {
			t.Fatalf("HasUnresumedPauseHitForInstance: got true after the only pause hit was resumed")
		}
	})
}
