// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

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

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/internal/pgtest"
)

// openMigratedDSN spins up a fresh Postgres container, runs all rimsky
// migrations against it, and returns the DSN the subscriber-under-test
// should use. Cleanup is via t.Cleanup hooks registered inside
// StartFreshPostgresDSN.
func openMigratedDSN(t *testing.T) string {
	t.Helper()
	dsn, terminate := pgtest.StartFreshPostgresDSN(context.Background(), t)
	t.Cleanup(terminate)

	d, err := persistence.Open(context.Background(), persistence.Config{
		Driver:   "postgres",
		Postgres: &persistence.PostgresConfig{DSN: dsn},
	})
	if err != nil {
		t.Fatalf("open driver: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := d.Migrate(context.Background(), shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return dsn
}

func TestSubscriber_PollsAndEmits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dsn := openMigratedDSN(t)

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	templateHash := "sha256-" + uuid.NewString()
	instanceID := uuid.New()
	frameID := uuid.New()
	_, err = pool.Exec(ctx,
		`INSERT INTO rimsky_templates (id, spec, state, registered_at)
		 VALUES ($1, '{}'::jsonb, 'deployed', now())`,
		templateHash)
	if err != nil {
		t.Fatalf("seed template: %v", err)
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO rimsky_instances (id, template_hash, instance_key, params, created_at)
		 VALUES ($1, $2, $3, '{}'::jsonb, now())`,
		instanceID, templateHash, "ck-"+uuid.NewString())
	if err != nil {
		t.Fatalf("seed instance: %v", err)
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO rimsky_frames (frame_id, instance_id, frame_resolution_mode, state, source_node_ids, queued_at, started_at, frame_timeout_ms)
		 VALUES ($1, $2, 'serial_queue', 'running', ARRAY[$1]::UUID[], now(), now(), 600000)`,
		frameID, instanceID)
	if err != nil {
		t.Fatalf("seed frame: %v", err)
	}

	leafRec := LeafRunRecord{
		RunID:             "run-1",
		NodeAlias:         "draft",
		TemplateNodeAlias: "draft",
		TemplateHash:      templateHash,
		ExecutorName:      "claude-agent",
		Changed:           true,
		LastOutcome:       "fresh_changed",
		TerminalKind:      "complete",
	}
	leafJSON, _ := json.Marshal(leafRec)
	_, err = pool.Exec(ctx,
		`INSERT INTO rimsky_lineage (id, record_kind, instance_id, frame_id, observed_at, record)
		 VALUES ($1, 'leaf_run', $2, $3, NOW() - INTERVAL '2 minutes', $4::jsonb)`,
		uuid.New(), instanceID, frameID, string(leafJSON))
	if err != nil {
		t.Fatalf("seed leaf_run: %v", err)
	}
	commitRec := ClaimTerminalRecord{
		ClaimHandleID: "claim-1",
		ProducerName:  "topics-ring",
		ScopeDataHash: "scope-1",
		ParentRunID:   "run-1",
		FrameID:       frameID.String(),
		Outcome:       "committed",
	}
	commitJSON, _ := json.Marshal(commitRec)
	_, err = pool.Exec(ctx,
		`INSERT INTO rimsky_lineage (id, record_kind, instance_id, frame_id, observed_at, record, outcome)
		 VALUES ($1, 'claim_terminal', $2, $3, NOW() - INTERVAL '1 minute', $4::jsonb, 'committed')`,
		uuid.New(), instanceID, frameID, string(commitJSON))
	if err != nil {
		t.Fatalf("seed claim_terminal: %v", err)
	}

	var (
		mu       sync.Mutex
		received []Event
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var ev Event
		_ = json.Unmarshal(body, &ev)
		mu.Lock()
		received = append(received, ev)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	sub, err := New(ctx, Config{
		RimskyDSN:    dsn,
		StateDSN:     dsn,
		BackendURL:   srv.URL,
		Namespace:    "ns-test",
		PollInterval: 50 * time.Millisecond,
		BatchSize:    100,
	}, logger)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sub.Close()

	if err := sub.tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}
	mu.Lock()
	got := len(received)
	mu.Unlock()
	if got != 2 {
		t.Errorf("received %d events; want 2", got)
	}

	if err := sub.tick(ctx); err != nil {
		t.Fatalf("tick (idempotent): %v", err)
	}
	mu.Lock()
	gotAfter := len(received)
	mu.Unlock()
	if gotAfter != 2 {
		t.Errorf("second tick added rows; received %d (want stays at 2)", gotAfter)
	}

	var stored time.Time
	if err := pool.QueryRow(ctx,
		`SELECT last_observed_at FROM rimsky_openlineage_cursor WHERE namespace = $1`,
		"ns-test",
	).Scan(&stored); err != nil {
		t.Fatalf("read cursor: %v", err)
	}
	if stored.IsZero() {
		t.Errorf("cursor was not persisted")
	}
}

func TestSubscriber_EmitFailureHaltsBatchAtFailingRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dsn := openMigratedDSN(t)

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	templateHash := "sha256-" + uuid.NewString()
	instanceID := uuid.New()
	frameID := uuid.New()
	_, _ = pool.Exec(ctx,
		`INSERT INTO rimsky_templates (id, spec, state, registered_at)
		 VALUES ($1, '{}'::jsonb, 'deployed', now())`,
		templateHash)
	_, _ = pool.Exec(ctx,
		`INSERT INTO rimsky_instances (id, template_hash, instance_key, params, created_at)
		 VALUES ($1, $2, $3, '{}'::jsonb, now())`,
		instanceID, templateHash, "ck-"+uuid.NewString())
	_, _ = pool.Exec(ctx,
		`INSERT INTO rimsky_frames (frame_id, instance_id, frame_resolution_mode, state, source_node_ids, queued_at, started_at, frame_timeout_ms)
		 VALUES ($1, $2, 'serial_queue', 'running', ARRAY[$1]::UUID[], now(), now(), 600000)`,
		frameID, instanceID)

	leafJSON, _ := json.Marshal(LeafRunRecord{RunID: "r", NodeAlias: "n", TemplateNodeAlias: "n"})
	for i := 0; i < 3; i++ {
		_, err := pool.Exec(ctx,
			`INSERT INTO rimsky_lineage (id, record_kind, instance_id, frame_id, observed_at, record)
			 VALUES ($1, 'leaf_run', $2, $3, NOW() - INTERVAL '1 minute' + make_interval(secs => $4), $5::jsonb)`,
			uuid.New(), instanceID, frameID, float64(i), string(leafJSON))
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	sub, err := New(ctx, Config{
		RimskyDSN:    dsn,
		StateDSN:     dsn,
		BackendURL:   srv.URL,
		Namespace:    "ns-fail",
		PollInterval: 50 * time.Millisecond,
		BatchSize:    100,
	}, logger)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sub.Close()

	_ = sub.tick(ctx)
	if !sub.cursorAt.Equal(time.Unix(0, 0)) {
		t.Errorf("cursor advanced despite emit failure: %v", sub.cursorAt)
	}
}
