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
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/internal/pgtest"
)

// seedInstanceWithMainScope inserts a paired (rimsky_instances,
// rimsky_run_scopes) tuple. Post-RunScope-first migration both columns
// are NOT NULL and reference each other; the FK pair is DEFERRABLE
// INITIALLY DEFERRED so the inserts must run inside a single tx.
func seedInstanceWithMainScope(t *testing.T, ctx context.Context, pool *pgxpool.Pool, instanceID uuid.UUID, templateHash string) {
	t.Helper()
	mainScopeID := uuid.New()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET CONSTRAINTS ALL DEFERRED`); err != nil {
		t.Fatalf("defer constraints: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO rimsky_instances (id, template_hash, instance_key, params, created_at, main_run_scope_id)
		 VALUES ($1, $2, $3, '{}'::jsonb, now(), $4)`,
		instanceID, templateHash, "ck-"+uuid.NewString(), mainScopeID); err != nil {
		t.Fatalf("seed instance: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO rimsky_run_scopes (id, graph_name, partition_key, instance_id)
		 VALUES ($1, 'main', '', $2)`,
		mainScopeID, instanceID); err != nil {
		t.Fatalf("seed run_scope: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

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
	seedInstanceWithMainScope(t, ctx, pool, instanceID, templateHash)
	_, err = pool.Exec(ctx,
		`INSERT INTO rimsky_frames (frame_id, instance_id, frame_resolution_mode, state, source_node_ids, queued_at, started_at, frame_timeout_ms)
		 VALUES ($1, $2, 'serial_queue', 'running', ARRAY[$1]::UUID[], now(), now(), 600000)`,
		frameID, instanceID)
	if err != nil {
		t.Fatalf("seed frame: %v", err)
	}

	leafRec := LeafRunRecord{
		RunID:              "run-1",
		NodeAlias:          "draft",
		TemplateNodeAlias:  "draft",
		TemplateHash:       templateHash,
		ExecutorName:       "claude-agent",
		Changed:            true,
		SettlingSignalType: "terminal/success",
		TerminalKind:       "complete",
	}
	leafJSON, _ := json.Marshal(leafRec)
	_, err = pool.Exec(ctx,
		`INSERT INTO rimsky_lineage (id, record_kind, instance_id, frame_id, observed_at, record, outcome)
		 VALUES ($1, 'leaf_run', $2, $3, NOW() - INTERVAL '2 minutes', $4::jsonb, '')`,
		uuid.New(), instanceID, frameID, string(leafJSON))
	if err != nil {
		t.Fatalf("seed leaf_run: %v", err)
	}
	commitRec := ClaimTerminalRecord{
		ClaimHandleID:     "claim-1",
		ProducerName:      "topics-ring",
		ScopeDataHash:     "scope-1",
		OpenLineageRunRef: "run-1",
		FrameID:           frameID.String(),
		Outcome:           "committed",
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
	seedInstanceWithMainScope(t, ctx, pool, instanceID, templateHash)
	_, _ = pool.Exec(ctx,
		`INSERT INTO rimsky_frames (frame_id, instance_id, frame_resolution_mode, state, source_node_ids, queued_at, started_at, frame_timeout_ms)
		 VALUES ($1, $2, 'serial_queue', 'running', ARRAY[$1]::UUID[], now(), now(), 600000)`,
		frameID, instanceID)

	leafJSON, _ := json.Marshal(LeafRunRecord{RunID: "r", NodeAlias: "n", TemplateNodeAlias: "n"})
	for i := 0; i < 3; i++ {
		_, err := pool.Exec(ctx,
			`INSERT INTO rimsky_lineage (id, record_kind, instance_id, frame_id, observed_at, record, outcome)
			 VALUES ($1, 'leaf_run', $2, $3, NOW() - INTERVAL '1 minute' + make_interval(secs => $4), $5::jsonb, '')`,
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

// TestSubscriber_DecodeFailureAdvancesCursor pins the post-2026-05-17
// behavior: undecodable rows (`toEvent` returns an error) advance the
// cursor instead of stalling the polling loop. A subsequent row that
// is decodable still emits successfully.
func TestSubscriber_DecodeFailureAdvancesCursor(t *testing.T) {
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
	seedInstanceWithMainScope(t, ctx, pool, instanceID, templateHash)
	_, _ = pool.Exec(ctx,
		`INSERT INTO rimsky_frames (frame_id, instance_id, frame_resolution_mode, state, source_node_ids, queued_at, started_at, frame_timeout_ms)
		 VALUES ($1, $2, 'serial_queue', 'running', ARRAY[$1]::UUID[], now(), now(), 600000)`,
		frameID, instanceID)

	// Row 1: undecodable (record_kind set but `record` is not JSON
	// shaped for any known kind — wire envelope is JSON but the
	// per-kind decode will fail because the record itself is invalid
	// JSON). We force the failure by inserting raw text that isn't
	// JSON in the JSONB column. Postgres accepts the column as JSON
	// because we cast `'not-json'::jsonb` — instead use an unknown
	// record_kind so `toEvent` falls through to the `default` case.
	_, err = pool.Exec(ctx,
		`INSERT INTO rimsky_lineage (id, record_kind, instance_id, frame_id, observed_at, record, outcome)
		 VALUES ($1, 'leaf_run', $2, $3, NOW() - INTERVAL '2 minutes', $4::jsonb, '')`,
		uuid.New(), instanceID, frameID, `{}`)
	if err != nil {
		t.Fatalf("seed bad row: %v", err)
	}
	// To force a decode failure we wedge a row with an unknown
	// record_kind via direct UPDATE — the CHECK constraint normally
	// blocks this, so drop it for the test.
	if _, err := pool.Exec(ctx,
		`ALTER TABLE rimsky_lineage DROP CONSTRAINT rimsky_lineage_record_kind_check`); err != nil {
		t.Fatalf("drop check: %v", err)
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO rimsky_lineage (id, record_kind, instance_id, frame_id, observed_at, record, outcome)
		 VALUES ($1, 'unknown_kind', $2, $3, NOW() - INTERVAL '1 minute', $4::jsonb, '')`,
		uuid.New(), instanceID, frameID, `{}`)
	if err != nil {
		t.Fatalf("seed unknown_kind row: %v", err)
	}
	// Row 3: a normal leaf_run that should emit successfully after
	// the cursor advances past the decode failure.
	leafJSON, _ := json.Marshal(LeafRunRecord{RunID: "r", NodeAlias: "n", TemplateNodeAlias: "n"})
	_, err = pool.Exec(ctx,
		`INSERT INTO rimsky_lineage (id, record_kind, instance_id, frame_id, observed_at, record, outcome)
		 VALUES ($1, 'leaf_run', $2, $3, NOW(), $4::jsonb, '')`,
		uuid.New(), instanceID, frameID, string(leafJSON))
	if err != nil {
		t.Fatalf("seed trailing row: %v", err)
	}

	var (
		mu       sync.Mutex
		received int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		received++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	sub, err := New(ctx, Config{
		RimskyDSN:    dsn,
		StateDSN:     dsn,
		BackendURL:   srv.URL,
		Namespace:    "ns-decode",
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
	got := received
	mu.Unlock()
	// Two leaf_run rows + one unknown_kind. The unknown_kind row is
	// undecodable but the cursor advances past it, and the trailing
	// leaf_run still emits. Expect 2 emit calls.
	if got != 2 {
		t.Errorf("received emits: got %d want 2", got)
	}
	// Cursor must have advanced past the trailing row's observed_at.
	if sub.cursorAt.Equal(time.Unix(0, 0)) {
		t.Errorf("cursor did not advance past undecodable row")
	}
}

// TestSubscriber_TieBreakerSameObservedAt pins the cycle-2 fix: when two
// `rimsky_lineage` rows share the same `observed_at` (no UNIQUE on the
// column), the cursor advances by `(observed_at, id)` tuple so the
// second row is NOT skipped on the next tick. Prior to the fix the
// cursor stored only `observed_at` and the second row's `> $1` filter
// dropped it permanently.
func TestSubscriber_TieBreakerSameObservedAt(t *testing.T) {
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
	seedInstanceWithMainScope(t, ctx, pool, instanceID, templateHash)
	_, _ = pool.Exec(ctx,
		`INSERT INTO rimsky_frames (frame_id, instance_id, frame_resolution_mode, state, source_node_ids, queued_at, started_at, frame_timeout_ms)
		 VALUES ($1, $2, 'serial_queue', 'running', ARRAY[$1]::UUID[], now(), now(), 600000)`,
		frameID, instanceID)

	leafJSON, _ := json.Marshal(LeafRunRecord{RunID: "r", NodeAlias: "n", TemplateNodeAlias: "n"})
	// Insert THREE rows with the same `observed_at` (down to the
	// microsecond). Pre-fix, cursor advancement would skip rows 2 + 3
	// after row 1 emitted.
	for i := 0; i < 3; i++ {
		_, err := pool.Exec(ctx,
			`INSERT INTO rimsky_lineage (id, record_kind, instance_id, frame_id, observed_at, record, outcome)
			 VALUES ($1, 'leaf_run', $2, $3, '2026-05-17 12:00:00.000000+00'::timestamptz, $4::jsonb, '')`,
			uuid.New(), instanceID, frameID, string(leafJSON))
		if err != nil {
			t.Fatalf("insert row %d: %v", i, err)
		}
	}

	var (
		mu       sync.Mutex
		received int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		received++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	// BatchSize=1 forces three separate ticks so the regression surface
	// (cursor advance between ticks) is exercised end-to-end.
	sub, err := New(ctx, Config{
		RimskyDSN:    dsn,
		StateDSN:     dsn,
		BackendURL:   srv.URL,
		Namespace:    "ns-tie",
		PollInterval: 50 * time.Millisecond,
		BatchSize:    1,
	}, logger)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sub.Close()

	for i := 0; i < 3; i++ {
		if err := sub.tick(ctx); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
	}
	mu.Lock()
	got := received
	mu.Unlock()
	if got != 3 {
		t.Errorf("received emits: got %d want 3 — cursor lost a tie-row", got)
	}
}

// TestSubscriber_CursorMigrationZeroUUIDRepair pins the post-2026-05-17
// fix: when `ensureCursorTable` finds a pre-cycle-2 cursor row with
// `last_id = 00000000-...` (the ADD COLUMN default), it bumps the row
// to `last_id = ffffffff-...` so the predicate `(observed_at, id) >
// ($1, $2)` does not match every row at the same observed_at as the
// cursor. Without the bump, restart on a pre-cycle-2 install would
// re-emit every row at the cursor's observed_at (any non-zero UUID is
// strictly greater than the zero UUID).
func TestSubscriber_CursorMigrationZeroUUIDRepair(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dsn := openMigratedDSN(t)

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	// Pre-create the cursor table the way a pre-cycle-2 install would
	// have left it: row exists at a non-epoch `last_observed_at` AND
	// `last_id` is the zero UUID (column default). Drop and recreate
	// without the `last_id` column to simulate the pre-migration state.
	if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS rimsky_openlineage_cursor`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE TABLE rimsky_openlineage_cursor (
		    namespace          TEXT PRIMARY KEY,
		    last_observed_at   TIMESTAMPTZ NOT NULL,
		    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		t.Fatalf("create legacy: %v", err)
	}
	cursorTime := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx,
		`INSERT INTO rimsky_openlineage_cursor (namespace, last_observed_at)
		 VALUES ($1, $2)`,
		"ns-repair", cursorTime); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	// Construct the subscriber — its `ensureCursorTable` should fire
	// the ALTER + UPDATE migration path.
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	sub, err := New(ctx, Config{
		RimskyDSN:    dsn,
		StateDSN:     dsn,
		BackendURL:   "http://127.0.0.1:0", // not used; no tick fires
		Namespace:    "ns-repair",
		PollInterval: 1 * time.Second,
		BatchSize:    10,
	}, logger)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sub.Close()

	// After New, the legacy row should have last_id = ffffffff-...
	var lastID string
	if err := pool.QueryRow(ctx,
		`SELECT last_id::text FROM rimsky_openlineage_cursor WHERE namespace = $1`,
		"ns-repair",
	).Scan(&lastID); err != nil {
		t.Fatalf("read last_id: %v", err)
	}
	if lastID != "ffffffff-ffff-ffff-ffff-ffffffffffff" {
		t.Errorf("post-migration last_id: got %q want ffffffff-ffff-ffff-ffff-ffffffffffff", lastID)
	}
}

// TestLeafRunRecord_WireContract pins the writer→subscriber JSON
// contract for `record_kind = 'leaf_run'`. The writer-side
// `runtime/lineage_writer.go::LeafRunRecord` is the canonical shape;
// every json field it emits must round-trip through the subscriber's
// `LeafRunRecord` decode without loss. The JSON below is the exact
// emit shape of the writer-side struct as of cycle 5; if the writer
// adds a field, mirror it on the subscriber side and update this
// payload accordingly.
//
// Pins the cycle-5 alignment fix (NodeID, FrameID, ScopeDataHash,
// State, ErrorClass, SubstitutionRefs, Extra). Post-2026-05-17 the
// SubstitutionRefs shape is the richer `[{source_kind, source_node_alias,
// source_version_or_id}]` object form (the `[]string` form was dropped
// in cycle 6); the per-entry object lets the ancestor walker
// discriminate `source_kind=run` (with a UUID `source_version_or_id`)
// from `source_kind=attribute`/`event` (informational name).
func TestLeafRunRecord_WireContract(t *testing.T) {
	t.Parallel()
	const writerJSON = `{
	  "run_id": "11111111-1111-1111-1111-111111111111",
	  "node_id": "22222222-2222-2222-2222-222222222222",
	  "frame_id": "33333333-3333-3333-3333-333333333333",
	  "child_key": "partition-0",
	  "node_alias": "stage",
	  "parent_run_id": "44444444-4444-4444-4444-444444444444",
	  "frame_trigger_kind": "invalidate",
	  "trigger_message_id": "55555555-5555-5555-5555-555555555555",
	  "held_claims": [
	    {"claim_handle_id":"c1","role":"acquire","producer_name":"p","scope_data_hash":"s"}
	  ],
	  "executor_name": "claude-agent",
	  "executor_version": "1.2.3",
	  "template_hash": "sha256-aaa",
	  "template_node_alias": "stage",
	  "params_snapshot_hash": "sha256-bbb",
	  "attributes_hash": "sha256-ccc",
	  "scope_data_hash": "sha256-ddd",
	  "state": "fresh",
	  "settling_signal_type": "terminal/success",
	  "changed": true,
	  "terminal_kind": "complete",
	  "error_class": "",
	  "substitution_refs": [
	    {"source_kind":"attribute","source_node_alias":"alias-a","source_version_or_id":"path-1"},
	    {"source_kind":"run","source_node_alias":"alias-a","source_version_or_id":"66666666-6666-6666-6666-666666666666"}
	  ],
	  "extra": {"k": "v"}
	}`
	var rec LeafRunRecord
	if err := json.Unmarshal([]byte(writerJSON), &rec); err != nil {
		t.Fatalf("decode writer JSON: %v", err)
	}
	if rec.RunID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("RunID = %q", rec.RunID)
	}
	if rec.NodeID != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("NodeID = %q", rec.NodeID)
	}
	if rec.FrameID != "33333333-3333-3333-3333-333333333333" {
		t.Errorf("FrameID = %q", rec.FrameID)
	}
	if rec.ScopeDataHash != "sha256-ddd" {
		t.Errorf("ScopeDataHash = %q", rec.ScopeDataHash)
	}
	if rec.State != "fresh" {
		t.Errorf("State = %q", rec.State)
	}
	if rec.ErrorClass != "" {
		t.Errorf("ErrorClass = %q want empty", rec.ErrorClass)
	}
	if len(rec.SubstitutionRefs) != 2 {
		t.Fatalf("SubstitutionRefs length = %d want 2", len(rec.SubstitutionRefs))
	}
	if rec.SubstitutionRefs[0].SourceKind != "attribute" || rec.SubstitutionRefs[0].SourceNodeAlias != "alias-a" || rec.SubstitutionRefs[0].SourceVersionOrID != "path-1" {
		t.Errorf("SubstitutionRefs[0] = %+v", rec.SubstitutionRefs[0])
	}
	if rec.SubstitutionRefs[1].SourceKind != "run" || rec.SubstitutionRefs[1].SourceVersionOrID != "66666666-6666-6666-6666-666666666666" {
		t.Errorf("SubstitutionRefs[1] = %+v", rec.SubstitutionRefs[1])
	}
	if rec.Extra["k"] != "v" {
		t.Errorf("Extra = %+v", rec.Extra)
	}
}

// TestClaimTerminalRecord_WireContract pins the writer→subscriber
// JSON contract for `record_kind = 'claim_terminal'`. The writer
// emits `open_lineage_run_ref`; the subscriber must decode it onto
// the same field. Older payloads keyed by `parent_run_id` no longer
// flow — the renaming landed in cycle 5.
//
// Post-2026-05-17 (cycle 6): every field the writer-side
// `runtime/lineage_writer.go::ClaimTerminalRecord` emits — including
// `run_id`, `node_id`, `parent_claim_handle_id`, `producer_metadata` —
// must round-trip through the subscriber's decode without loss AND must
// surface in the emitted OpenLineage event's `rimsky` facet.
func TestClaimTerminalRecord_WireContract(t *testing.T) {
	t.Parallel()
	const writerJSON = `{
	  "claim_handle_id": "11111111-1111-1111-1111-111111111111",
	  "run_id": "22222222-2222-2222-2222-222222222222",
	  "node_id": "33333333-3333-3333-3333-333333333333",
	  "frame_id": "44444444-4444-4444-4444-444444444444",
	  "parent_claim_handle_id": "55555555-5555-5555-5555-555555555555",
	  "open_lineage_run_ref": "22222222-2222-2222-2222-222222222222",
	  "sub_claim_handle_ids": ["c1", "c2"],
	  "committed_at": "2026-05-17T00:00:00Z",
	  "producer_name": "p",
	  "scope_data_hash": "sha256-eee",
	  "version_id": "v-1",
	  "outcome": "committed",
	  "producer_metadata": {"region": "us-west-1", "shard": 7}
	}`
	var rec ClaimTerminalRecord
	if err := json.Unmarshal([]byte(writerJSON), &rec); err != nil {
		t.Fatalf("decode writer JSON: %v", err)
	}
	if rec.OpenLineageRunRef != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("OpenLineageRunRef = %q", rec.OpenLineageRunRef)
	}
	if rec.ScopeDataHash != "sha256-eee" {
		t.Errorf("ScopeDataHash = %q", rec.ScopeDataHash)
	}
	if rec.Outcome != "committed" {
		t.Errorf("Outcome = %q", rec.Outcome)
	}
	if rec.FrameID != "44444444-4444-4444-4444-444444444444" {
		t.Errorf("FrameID = %q", rec.FrameID)
	}
	if rec.RunID != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("RunID = %q", rec.RunID)
	}
	if rec.NodeID != "33333333-3333-3333-3333-333333333333" {
		t.Errorf("NodeID = %q", rec.NodeID)
	}
	if rec.ParentClaimHandleID != "55555555-5555-5555-5555-555555555555" {
		t.Errorf("ParentClaimHandleID = %q", rec.ParentClaimHandleID)
	}
	if rec.ProducerMetadata["region"] != "us-west-1" {
		t.Errorf("ProducerMetadata = %+v", rec.ProducerMetadata)
	}

	// Surface check: MakeClaimTerminalEvent must propagate every new
	// field into the OpenLineage event's `rimsky` facet so downstream
	// consumers can audit-trace the terminal back to its run + parent
	// claim chain.
	ev := MakeClaimTerminalEvent(rec, time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC), "ns-test")
	facets, ok := ev.Facets["rimsky"].(map[string]any)
	if !ok {
		t.Fatalf("rimsky facet block missing or wrong type: %+v", ev.Facets)
	}
	if facets["run_id"] != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("facet run_id = %v", facets["run_id"])
	}
	if facets["node_id"] != "33333333-3333-3333-3333-333333333333" {
		t.Errorf("facet node_id = %v", facets["node_id"])
	}
	if facets["parent_claim_handle_id"] != "55555555-5555-5555-5555-555555555555" {
		t.Errorf("facet parent_claim_handle_id = %v", facets["parent_claim_handle_id"])
	}
	pm, ok := facets["producer_metadata"].(map[string]any)
	if !ok || pm["region"] != "us-west-1" {
		t.Errorf("facet producer_metadata = %+v", facets["producer_metadata"])
	}
}

// TestLeafRunRecord_TagDisciplineAndOrder pins the JSON-tag discipline
// (required vs `omitempty`) AND the field-declaration order on the
// subscriber's LeafRunRecord. The writer-side
// `runtime/lineage_writer.go::LeafRunRecord` is canonical; this test
// captures the agreed shape so a unilateral subscriber edit cannot
// drift the wire contract without breaking here. Any mismatch (tag
// flip, field reorder, new field) requires updating both sides plus
// this test in the same commit.
func TestLeafRunRecord_TagDisciplineAndOrder(t *testing.T) {
	t.Parallel()
	want := []struct {
		field     string
		jsonTag   string
		omitempty bool
	}{
		{"RunID", "run_id", false},
		{"NodeID", "node_id", false},
		{"FrameID", "frame_id", false},
		{"ChildKey", "child_key", true},
		{"NodeAlias", "node_alias", true},
		{"ParentRunID", "parent_run_id", true},
		{"FrameTriggerKind", "frame_trigger_kind", true},
		{"TriggerMessageID", "trigger_message_id", true},
		{"HeldClaims", "held_claims", true},
		{"ExecutorName", "executor_name", true},
		{"ExecutorVersion", "executor_version", true},
		{"TemplateHash", "template_hash", true},
		{"TemplateNodeAlias", "template_node_alias", true},
		{"ParamsSnapshotHash", "params_snapshot_hash", true},
		{"AttributesHash", "attributes_hash", true},
		{"ScopeDataHash", "scope_data_hash", true},
		{"State", "state", false},
		{"SettlingSignalType", "settling_signal_type", false},
		{"Changed", "changed", true},
		{"TerminalKind", "terminal_kind", true},
		{"ErrorClass", "error_class", true},
		{"SubstitutionRefs", "substitution_refs", true},
		{"Extra", "extra", true},
	}
	assertStructJSONShape(t, LeafRunRecord{}, want)
}

// TestClaimTerminalRecord_TagDisciplineAndOrder pins the JSON-tag
// discipline (required vs `omitempty`) AND the field-declaration
// order on the subscriber's ClaimTerminalRecord. Same rationale as
// TestLeafRunRecord_TagDisciplineAndOrder above.
func TestClaimTerminalRecord_TagDisciplineAndOrder(t *testing.T) {
	t.Parallel()
	want := []struct {
		field     string
		jsonTag   string
		omitempty bool
	}{
		{"ClaimHandleID", "claim_handle_id", false},
		{"RunID", "run_id", false},
		{"NodeID", "node_id", false},
		{"FrameID", "frame_id", false},
		{"ParentClaimHandleID", "parent_claim_handle_id", true},
		{"OpenLineageRunRef", "open_lineage_run_ref", true},
		{"SubClaimHandleIDs", "sub_claim_handle_ids", true},
		{"CommittedAt", "committed_at", true},
		{"ProducerName", "producer_name", true},
		{"ScopeDataHash", "scope_data_hash", true},
		{"VersionID", "version_id", true},
		{"Outcome", "outcome", false},
		{"Cause", "cause", true},
		{"ProducerMetadata", "producer_metadata", true},
	}
	assertStructJSONShape(t, ClaimTerminalRecord{}, want)
}

// assertStructJSONShape verifies that v's exported struct fields match
// the expected list both in declaration order, in json-tag name, and
// in `omitempty` discipline.
func assertStructJSONShape(t *testing.T, v any, want []struct {
	field     string
	jsonTag   string
	omitempty bool
}) {
	t.Helper()
	rt := reflect.TypeOf(v)
	if rt.NumField() != len(want) {
		t.Fatalf("%s: NumField = %d, want %d (fields drifted; update both sides + this test)",
			rt.Name(), rt.NumField(), len(want))
	}
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		w := want[i]
		if f.Name != w.field {
			t.Errorf("%s field[%d]: name = %q, want %q (order drifted)", rt.Name(), i, f.Name, w.field)
			continue
		}
		tag := f.Tag.Get("json")
		gotName, gotOmit := parseJSONTag(tag)
		if gotName != w.jsonTag {
			t.Errorf("%s.%s json tag name = %q, want %q", rt.Name(), f.Name, gotName, w.jsonTag)
		}
		if gotOmit != w.omitempty {
			t.Errorf("%s.%s omitempty = %v, want %v", rt.Name(), f.Name, gotOmit, w.omitempty)
		}
	}
}

// parseJSONTag splits a `json:"field,omitempty"` tag value into
// (name, omitempty). Returns (tag, false) when no comma is present.
func parseJSONTag(tag string) (name string, omitempty bool) {
	parts := strings.Split(tag, ",")
	if len(parts) == 0 {
		return tag, false
	}
	name = parts[0]
	for _, opt := range parts[1:] {
		if opt == "omitempty" {
			omitempty = true
		}
	}
	return name, omitempty
}
