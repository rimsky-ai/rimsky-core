// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: breakpoint

package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	sqlitepersist "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func openSQLiteDriver(t *testing.T) persistence.Database {
	t.Helper()
	dir := t.TempDir()
	d, err := persistence.Open(context.Background(), persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(dir, "bp.db")},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := d.Migrate(context.Background(), shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return d
}

func seedBreakpointFixture(t *testing.T, ctx context.Context, d persistence.Database) shared.UUID {
	t.Helper()
	store := d.Tables()
	templateHash := "sha256-" + uuid.NewString()
	instanceID := uuid.New()
	mainRunScopeID := uuid.New()

	tmpl := spec.TemplateSpec{
		Name:           "breakpoint-fixture",
		Version:        "1",
		FrameTimeoutMs: 600000,
		Nodes: []spec.TemplateNodeDef{
			{Type: "fixture-node-type", Executor: "test-executor"},
		},
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID:     templateHash,
			Spec:   tmpl,
			State:  persistence.TemplateStateRegistered,
			Source: "direct",
		}, tx); err != nil {
			return err
		}
		if err := store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:         mainRunScopeID,
			GraphName:  spec.MainGraphName,
			InstanceID: instanceID,
		}); err != nil {
			return err
		}
		if _, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID:             instanceID,
			TemplateHash:   templateHash,
			MainRunScopeID: mainRunScopeID,
		}, tx); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("seedBreakpointFixture: %v", err)
	}
	return instanceID
}

func newBreakpoint(instanceID shared.UUID, customise func(*persistence.BreakpointRow)) persistence.BreakpointRow {
	bp := persistence.BreakpointRow{
		InstanceID:     instanceID,
		Matcher:        map[string]any{"node_type": "x"},
		Checkpoint:     persistence.CheckpointBeforeDispatch,
		Mode:           persistence.BreakpointModePause,
		OverflowPolicy: persistence.OverflowDropOldest,
		HitTTLSeconds:  300,
		CreatedByKey:   "test-key",
	}
	if customise != nil {
		customise(&bp)
	}
	return bp
}

func makeHit(bpID, instanceID shared.UUID, customise func(*persistence.BreakpointHitRow)) persistence.BreakpointHitRow {
	h := persistence.BreakpointHitRow{
		BreakpointID: bpID,
		InstanceID:   instanceID,
		Checkpoint:   persistence.CheckpointBeforeDispatch,
		Mode:         persistence.BreakpointModePause,
		Snapshot:     map[string]any{"node_type": "x"},
	}
	if customise != nil {
		customise(&h)
	}
	return h
}

func createBreakpoint(t *testing.T, ctx context.Context, store persistence.Tables, bp persistence.BreakpointRow) shared.UUID {
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

func forceExpired(t *testing.T, ctx context.Context, d persistence.Database, bpID shared.UUID) {
	t.Helper()
	db := sqlitepersist.DBFromDatabase(d)
	past := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := db.ExecContext(ctx,
		`UPDATE rimsky_instance_breakpoints SET expires_at = ? WHERE id = ?`,
		past, bpID.String()); err != nil {
		t.Fatalf("forceExpired: %v", err)
	}
}

func TestSQLiteBreakpoints_CreateGetRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openSQLiteDriver(t)
	store := d.Tables()
	instanceID := seedBreakpointFixture(t, ctx, d)

	signal := "ProducerOpened"
	ttl := 600
	want := newBreakpoint(instanceID, func(b *persistence.BreakpointRow) {
		b.Matcher = map[string]any{"node_type": "x", "signal_type": "ProducerOpened"}
		b.Checkpoint = persistence.CheckpointAfterTerminal
		b.SignalType = &signal
		b.Mode = persistence.BreakpointModeNotifyOnly
		b.OverflowPolicy = persistence.OverflowAutoResumeAfterTTL
		b.HitTTLSeconds = 120
		b.TTLSeconds = &ttl
		b.CreatedByKey = "operator-key"
	})

	id := createBreakpoint(t, ctx, store, want)
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
}

func TestSQLiteBreakpoints_GetNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openSQLiteDriver(t)
	store := d.Tables()
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
}

func TestSQLiteBreakpoints_ListForInstance_IncludeExpired(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openSQLiteDriver(t)
	store := d.Tables()
	instanceID := seedBreakpointFixture(t, ctx, d)

	activeTTL := 3600
	expiredTTL := 60
	activeID := createBreakpoint(t, ctx, store, newBreakpoint(instanceID, func(b *persistence.BreakpointRow) {
		b.TTLSeconds = &activeTTL
		b.Matcher = map[string]any{"label": "active"}
	}))
	expiredID := createBreakpoint(t, ctx, store, newBreakpoint(instanceID, func(b *persistence.BreakpointRow) {
		b.TTLSeconds = &expiredTTL
		b.Matcher = map[string]any{"label": "expired"}
	}))
	forceExpired(t, ctx, d, expiredID)

	var rows []persistence.BreakpointRow
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		rows, err = store.Breakpoints().ListForInstance(ctx, instanceID, false, tx)
		return err
	}); err != nil {
		t.Fatalf("ListForInstance(false): %v", err)
	}
	if len(rows) != 1 || rows[0].ID != activeID {
		t.Fatalf("includeExpired=false: got %d rows want 1 (id=%v)", len(rows), activeID)
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		rows, err = store.Breakpoints().ListForInstance(ctx, instanceID, true, tx)
		return err
	}); err != nil {
		t.Fatalf("ListForInstance(true): %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("includeExpired=true: got %d rows want 2", len(rows))
	}
}

func TestSQLiteBreakpoints_IncrementDroppedMonotonic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openSQLiteDriver(t)
	store := d.Tables()
	instanceID := seedBreakpointFixture(t, ctx, d)
	id := createBreakpoint(t, ctx, store, newBreakpoint(instanceID, nil))

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
}

func TestSQLiteBreakpoints_SweepExpired(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openSQLiteDriver(t)
	store := d.Tables()
	instanceID := seedBreakpointFixture(t, ctx, d)
	ttl := 600
	bp := newBreakpoint(instanceID, func(b *persistence.BreakpointRow) { b.TTLSeconds = &ttl })
	liveID := createBreakpoint(t, ctx, store, bp)
	deadID := createBreakpoint(t, ctx, store, bp)
	forceExpired(t, ctx, d, deadID)

	var n int
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		n, err = store.Breakpoints().SweepExpired(ctx, time.Now(), tx)
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
}

func TestSQLiteBreakpointHits_CreateReturnsIDAndMonotonicSeq(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openSQLiteDriver(t)
	store := d.Tables()
	instanceID := seedBreakpointFixture(t, ctx, d)
	bpID := createBreakpoint(t, ctx, store, newBreakpoint(instanceID, nil))

	var prevSeq int64 = -1
	for i := 0; i < 5; i++ {
		var (
			id  shared.UUID
			seq int64
		)
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var err error
			id, seq, err = store.BreakpointHits().Create(ctx, makeHit(bpID, instanceID, nil), tx)
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
}

func TestSQLiteBreakpointHits_ListSinceIncludesResumedRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openSQLiteDriver(t)
	store := d.Tables()
	instanceID := seedBreakpointFixture(t, ctx, d)
	bpID := createBreakpoint(t, ctx, store, newBreakpoint(instanceID, nil))

	var hitIDs [4]shared.UUID
	for i := 0; i < 4; i++ {
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			id, _, err := store.BreakpointHits().Create(ctx, makeHit(bpID, instanceID, nil), tx)
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
		return store.BreakpointHits().Resume(ctx, hitIDs[1], "operator", nil, tx)
	}); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	var rows []persistence.BreakpointHitRow
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		rows, err = store.BreakpointHits().ListSinceForInstance(ctx, instanceID, 0, 100, tx)
		return err
	}); err != nil {
		t.Fatalf("ListSinceForInstance: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("ListSinceForInstance: got %d rows want 4", len(rows))
	}
	for i := 1; i < len(rows); i++ {
		if rows[i].Seq <= rows[i-1].Seq {
			t.Errorf("seq not strictly ascending: %d then %d", rows[i-1].Seq, rows[i].Seq)
		}
	}
	var found bool
	for _, r := range rows {
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

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		rows, err = store.BreakpointHits().ListSinceForBreakpoint(ctx, bpID, 0, 100, tx)
		return err
	}); err != nil {
		t.Fatalf("ListSinceForBreakpoint: %v", err)
	}
	if len(rows) != 4 {
		t.Errorf("ListSinceForBreakpoint: got %d rows want 4", len(rows))
	}
}

func TestSQLiteBreakpointHits_ListUnresumedFilters(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openSQLiteDriver(t)
	store := d.Tables()
	instanceID := seedBreakpointFixture(t, ctx, d)
	bpID := createBreakpoint(t, ctx, store, newBreakpoint(instanceID, nil))

	var ids [3]shared.UUID
	for i := 0; i < 3; i++ {
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			id, _, err := store.BreakpointHits().Create(ctx, makeHit(bpID, instanceID, nil), tx)
			if err != nil {
				return err
			}
			ids[i] = id
			return nil
		}); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.BreakpointHits().Resume(ctx, ids[1], "operator", nil, tx)
	}); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	var unresumed []persistence.BreakpointHitRow
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		unresumed, err = store.BreakpointHits().ListUnresumedForBreakpoint(ctx, bpID, tx)
		return err
	}); err != nil {
		t.Fatalf("ListUnresumedForBreakpoint: %v", err)
	}
	if len(unresumed) != 2 {
		t.Fatalf("ListUnresumedForBreakpoint: got %d rows want 2", len(unresumed))
	}
	for _, r := range unresumed {
		if r.ResumedAt != nil {
			t.Errorf("unresumed list contained row with ResumedAt set: %v", r.ID)
		}
		if r.ID == ids[1] {
			t.Errorf("unresumed list contained resumed id %v", r.ID)
		}
	}
}

func TestSQLiteBreakpointHits_ResumeSetsFieldsAndIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openSQLiteDriver(t)
	store := d.Tables()
	instanceID := seedBreakpointFixture(t, ctx, d)
	bpID := createBreakpoint(t, ctx, store, newBreakpoint(instanceID, nil))

	var hitID shared.UUID
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		id, _, err := store.BreakpointHits().Create(ctx, makeHit(bpID, instanceID, nil), tx)
		hitID = id
		return err
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	overlay := map[string]any{"attr_overrides": map[string]any{"v": 1}}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.BreakpointHits().Resume(ctx, hitID, "operator", overlay, tx)
	}); err != nil {
		t.Fatalf("Resume: %v", err)
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

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.BreakpointHits().Resume(ctx, hitID, "different-operator", nil, tx)
	}); err != nil {
		t.Fatalf("Resume replay: %v", err)
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
		return store.BreakpointHits().Resume(ctx, uuid.New(), "operator", nil, tx)
	}); !errors.Is(err, shared.ErrBreakpointHitNotFound) {
		t.Errorf("Resume(missing): expected ErrBreakpointHitNotFound, got %v", err)
	}
}

func TestSQLiteBreakpointHits_AutoResumeStale(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openSQLiteDriver(t)
	store := d.Tables()
	instanceID := seedBreakpointFixture(t, ctx, d)

	autoID := createBreakpoint(t, ctx, store, newBreakpoint(instanceID, func(b *persistence.BreakpointRow) {
		b.OverflowPolicy = persistence.OverflowAutoResumeAfterTTL
		b.HitTTLSeconds = 1
	}))
	dropID := createBreakpoint(t, ctx, store, newBreakpoint(instanceID, func(b *persistence.BreakpointRow) {
		b.OverflowPolicy = persistence.OverflowDropOldest
		b.HitTTLSeconds = 1
	}))
	longTTLID := createBreakpoint(t, ctx, store, newBreakpoint(instanceID, func(b *persistence.BreakpointRow) {
		b.OverflowPolicy = persistence.OverflowAutoResumeAfterTTL
		b.HitTTLSeconds = 3600
	}))

	var autoHitID, dropHitID, longTTLHitID shared.UUID
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		ah, _, err := store.BreakpointHits().Create(ctx, makeHit(autoID, instanceID, nil), tx)
		if err != nil {
			return err
		}
		dh, _, err := store.BreakpointHits().Create(ctx, makeHit(dropID, instanceID, nil), tx)
		if err != nil {
			return err
		}
		lh, _, err := store.BreakpointHits().Create(ctx, makeHit(longTTLID, instanceID, nil), tx)
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
}

func TestSQLiteBreakpointHits_DropOldest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openSQLiteDriver(t)
	store := d.Tables()
	instanceID := seedBreakpointFixture(t, ctx, d)
	bpID := createBreakpoint(t, ctx, store, newBreakpoint(instanceID, nil))

	const total = 150
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		for i := 0; i < total; i++ {
			if _, _, err := store.BreakpointHits().Create(ctx, makeHit(bpID, instanceID, nil), tx); err != nil {
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
}

func TestSQLiteBreakpointHits_UnresumedCount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openSQLiteDriver(t)
	store := d.Tables()
	instanceID := seedBreakpointFixture(t, ctx, d)
	bpID := createBreakpoint(t, ctx, store, newBreakpoint(instanceID, nil))

	var ids [5]shared.UUID
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		for i := 0; i < 5; i++ {
			id, _, err := store.BreakpointHits().Create(ctx, makeHit(bpID, instanceID, nil), tx)
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
			return store.BreakpointHits().Resume(ctx, ids[idx], "op", nil, tx)
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
}

func TestSQLiteBreakpointHits_SweepOrphanedUnresumed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := openSQLiteDriver(t)
	store := d.Tables()
	instanceID := seedBreakpointFixture(t, ctx, d)

	blockBP := newBreakpoint(instanceID, func(b *persistence.BreakpointRow) {
		b.OverflowPolicy = persistence.OverflowBlockDispatch
	})
	autoBP := newBreakpoint(instanceID, func(b *persistence.BreakpointRow) {
		b.OverflowPolicy = persistence.OverflowAutoResumeAfterTTL
		b.HitTTLSeconds = 3600
	})
	blockID := createBreakpoint(t, ctx, store, blockBP)
	autoID := createBreakpoint(t, ctx, store, autoBP)

	var blockHitID, blockResumedID, autoHitID shared.UUID
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		bh, _, err := store.BreakpointHits().Create(ctx, makeHit(blockID, instanceID, nil), tx)
		if err != nil {
			return err
		}
		blockHitID = bh
		br, _, err := store.BreakpointHits().Create(ctx, makeHit(blockID, instanceID, nil), tx)
		if err != nil {
			return err
		}
		blockResumedID = br
		ah, _, err := store.BreakpointHits().Create(ctx, makeHit(autoID, instanceID, nil), tx)
		if err != nil {
			return err
		}
		autoHitID = ah
		return store.BreakpointHits().Resume(ctx, blockResumedID, "op", nil, tx)
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
		t.Errorf("SweepOrphanedUnresumed rowcount: got %d want 1", n)
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		got, err := store.BreakpointHits().Get(ctx, blockHitID, tx)
		if err != nil {
			return err
		}
		if got != nil {
			t.Errorf("unresumed block_dispatch hit should be reaped, got %+v", got)
		}
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
		t.Fatalf("post-sweep Get: %v", err)
	}
}
