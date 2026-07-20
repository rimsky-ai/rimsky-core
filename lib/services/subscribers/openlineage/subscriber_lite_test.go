// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

func setupLineageSchema(t *testing.T, dsn string) {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS rimsky_lineage (
		    id           UUID PRIMARY KEY,
		    record_kind  TEXT NOT NULL,
		    instance_id  UUID NOT NULL,
		    frame_id     UUID NOT NULL,
		    observed_at  TIMESTAMPTZ NOT NULL,
		    record       JSONB NOT NULL,
		    outcome      TEXT NOT NULL DEFAULT ''
		)`); err != nil {
		t.Fatalf("create rimsky_lineage: %v", err)
	}
}

func insertLineageRow(t *testing.T, dsn string, id uuid.UUID, observedAt time.Time, rec LeafRunRecord) {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO rimsky_lineage (id, record_kind, instance_id, frame_id, observed_at, record)
		VALUES ($1, 'leaf_run', $2, $3, $4, $5)`,
		id, uuid.New(), uuid.New(), observedAt, raw,
	); err != nil {
		t.Fatalf("insert lineage row: %v", err)
	}
}

func newLiteSubscriber(t *testing.T, dsn string, backendURL string, lagWindow time.Duration) *Subscriber {
	t.Helper()
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	sub, err := New(ctx, Config{
		RimskyDSN:    dsn,
		StateDSN:     dsn,
		BackendURL:   backendURL,
		Namespace:    "ns-lite",
		PollInterval: time.Hour,
		BatchSize:    100,
		LagWindow:    lagWindow,
	}, logger)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(sub.Close)
	return sub
}

func TestFetchSince_LagWindowWithholdsRowsUntilPeerTransactionsCouldHaveCommitted(t *testing.T) {
	ctx := context.Background()
	dsn := harness.StartFreshPostgres(ctx, t)
	setupLineageSchema(t, dsn)

	var (
		mu       sync.Mutex
		received []string
	)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var ev Event
		_ = json.Unmarshal(body, &ev)
		mu.Lock()
		received = append(received, ev.Run.RunID)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	lagWindow := 2 * time.Second
	sub := newLiteSubscriber(t, dsn, backend.URL, lagWindow)

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	idB := uuid.New()
	insertLineageRow(t, dsn, idB, t0, LeafRunRecord{RunID: idB.String(), State: "fresh"})

	sub.nowFn = func() time.Time { return t0.Add(500 * time.Millisecond) }
	if err := sub.tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}
	mu.Lock()
	gotEarly := len(received)
	mu.Unlock()
	if gotEarly != 0 {
		t.Fatalf("received %d events before the lag window elapsed (want 0); "+
			"a row inside the lag window must not be cursored past, or a later-committing "+
			"earlier-timestamped peer row would be silently skipped", gotEarly)
	}

	idA := uuid.New()
	insertLineageRow(t, dsn, idA, t0.Add(-100*time.Millisecond), LeafRunRecord{RunID: idA.String(), State: "fresh"})

	sub.nowFn = func() time.Time { return t0.Add(3 * time.Second) }
	if err := sub.tick(ctx); err != nil {
		t.Fatalf("tick (after lag window elapsed): %v", err)
	}

	mu.Lock()
	got := append([]string(nil), received...)
	mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("received %v (want both rows, in observed_at order, once the lag window let both settle)", got)
	}
	if got[0] != idA.String() || got[1] != idB.String() {
		t.Errorf("delivery order = %v, want [%s %s] (ascending observed_at)", got, idA, idB)
	}
}

func TestTick_PermanentRejectionDeadLettersAndAdvancesPastPoisonPill(t *testing.T) {
	ctx := context.Background()
	dsn := harness.StartFreshPostgres(ctx, t)
	setupLineageSchema(t, dsn)

	rejectedID := uuid.New()
	acceptedID := uuid.New()

	var (
		mu       sync.Mutex
		accepted []string
	)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var ev Event
		_ = json.Unmarshal(body, &ev)
		if ev.Run.RunID == rejectedID.String() {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		mu.Lock()
		accepted = append(accepted, ev.Run.RunID)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	sub := newLiteSubscriber(t, dsn, backend.URL, 0)

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	insertLineageRow(t, dsn, rejectedID, t0, LeafRunRecord{RunID: rejectedID.String(), State: "fresh"})
	insertLineageRow(t, dsn, acceptedID, t0.Add(time.Second), LeafRunRecord{RunID: acceptedID.String(), State: "fresh"})

	sub.nowFn = func() time.Time { return t0.Add(time.Hour) }
	if err := sub.tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}

	mu.Lock()
	gotAccepted := append([]string(nil), accepted...)
	mu.Unlock()
	if len(gotAccepted) != 1 || gotAccepted[0] != acceptedID.String() {
		t.Fatalf("accepted = %v, want [%s]; a permanently-rejected row must not head-of-line-block "+
			"delivery of the row behind it in the same tick", gotAccepted, acceptedID)
	}

	var (
		gotID         uuid.UUID
		statusCode    int
		reason        string
		deadLetterCnt int
	)
	row := sub.state.QueryRow(ctx,
		`SELECT id, status_code, reason FROM rimsky_openlineage_dead_letter WHERE namespace = $1`,
		sub.cfg.Namespace)
	if err := row.Scan(&gotID, &statusCode, &reason); err != nil {
		t.Fatalf("scan dead letter row: %v", err)
	}
	if gotID != rejectedID {
		t.Errorf("dead-lettered id = %s, want %s", gotID, rejectedID)
	}
	if statusCode != http.StatusUnprocessableEntity {
		t.Errorf("dead-lettered status_code = %d, want %d", statusCode, http.StatusUnprocessableEntity)
	}
	if reason == "" {
		t.Error("dead-lettered reason is empty")
	}
	if err := sub.state.QueryRow(ctx,
		`SELECT count(*) FROM rimsky_openlineage_dead_letter WHERE namespace = $1`,
		sub.cfg.Namespace).Scan(&deadLetterCnt); err != nil {
		t.Fatalf("count dead letters: %v", err)
	}
	if deadLetterCnt != 1 {
		t.Fatalf("dead letter count = %d, want 1", deadLetterCnt)
	}

	if !sub.cursorAt.Equal(t0.Add(time.Second)) || sub.cursorID != acceptedID {
		t.Errorf("cursor after tick = (%v, %s), want (%v, %s) — cursor must advance past the dead-lettered row",
			sub.cursorAt, sub.cursorID, t0.Add(time.Second), acceptedID)
	}

	mu.Lock()
	accepted = nil
	mu.Unlock()
	if err := sub.tick(ctx); err != nil {
		t.Fatalf("second tick: %v", err)
	}
	mu.Lock()
	gotSecond := len(accepted)
	mu.Unlock()
	if gotSecond != 0 {
		t.Errorf("second tick delivered %d more events (want 0; nothing new past the cursor)", gotSecond)
	}
}

func TestTick_TransientFailureHaltsBatchWithoutDeadLettering(t *testing.T) {
	ctx := context.Background()
	dsn := harness.StartFreshPostgres(ctx, t)
	setupLineageSchema(t, dsn)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer backend.Close()

	sub := newLiteSubscriber(t, dsn, backend.URL, 0)

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rowID := uuid.New()
	insertLineageRow(t, dsn, rowID, t0, LeafRunRecord{RunID: rowID.String(), State: "fresh"})

	sub.nowFn = func() time.Time { return t0.Add(time.Hour) }
	if err := sub.tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if !sub.cursorAt.Equal(time.Unix(0, 0)) {
		t.Errorf("cursor advanced despite a transient (5xx) failure: %v", sub.cursorAt)
	}
	var deadLetterCnt int
	if err := sub.state.QueryRow(ctx,
		`SELECT count(*) FROM rimsky_openlineage_dead_letter WHERE namespace = $1`,
		sub.cfg.Namespace).Scan(&deadLetterCnt); err != nil {
		t.Fatalf("count dead letters: %v", err)
	}
	if deadLetterCnt != 0 {
		t.Errorf("dead letter count = %d, want 0 (a transient failure must not be dead-lettered)", deadLetterCnt)
	}
}
