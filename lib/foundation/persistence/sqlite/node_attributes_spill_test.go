// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package sqlite_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	sqlitedrv "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
)

// TestNodeAttributesSpillRoundtrip exercises D6/D7 wiring against the
// SQLite driver + the in-memory BlobBackend, post-per-run keying:
//   - small payload (below threshold) stored inline; value_handle is NULL.
//   - large payload (above threshold) spilled; value_handle non-NULL,
//     `data` reset to '{}', read transparently dereferences.
//   - overwriting a spilled row inserts the prior handle into
//     rimsky_blob_orphans.
//   - downgrading from spilled to inline clears value_handle and queues
//     the prior handle as an orphan.
//   - MergeDelta against a spilled row materializes, merges, re-spills.
//
// Per-run keying means each Upsert needs a distinct `runID` if it
// represents a distinct run, or the same `runID` to overwrite. Here we
// exercise overwrite semantics on a single run.
func TestNodeAttributesSpillRoundtrip(t *testing.T) {
	t.Setenv(persistence.ProcessRoleEnv, "unified")
	d := openSQLite(t)
	ctx := context.Background()
	rawDB := sqlitedrv.DBFromDatabase(d)

	mem := persistence.NewMemoryBackend()
	// @constraint: spill threshold is 256 bytes; small payloads below stay inline, large above spill.
	d.SetBlobBackend(mem, 256, time.Hour)

	store := d.Tables()
	attrs := store.NodeAttributes()
	orphans := store.BlobOrphans()

	nodeID, runID := seedFixtureNodeAndRun(t, rawDB)

	small := map[string]any{"k": "v"}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return attrs.Upsert(ctx, runID, nodeID, small, tx)
	}); err != nil {
		t.Fatalf("Upsert small: %v", err)
	}
	verifyNoSpill(t, rawDB, runID)

	got := readData(t, store, runID)
	if got["k"] != "v" {
		t.Fatalf("Get small: got %v, want k=v", got)
	}

	bigVal := strings.Repeat("x", 500)
	large := map[string]any{"big": bigVal, "tag": "first"}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return attrs.Upsert(ctx, runID, nodeID, large, tx)
	}); err != nil {
		t.Fatalf("Upsert large: %v", err)
	}
	firstHandle := verifySpill(t, rawDB, runID, mem.Name())

	got = readData(t, store, runID)
	if got["big"] != bigVal {
		t.Fatalf("Get large: big mismatch")
	}
	if got["tag"] != "first" {
		t.Fatalf("Get large: tag=%v, want first", got["tag"])
	}

	large2 := map[string]any{"big": bigVal, "tag": "second"}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return attrs.Upsert(ctx, runID, nodeID, large2, tx)
	}); err != nil {
		t.Fatalf("Upsert large2: %v", err)
	}
	secondHandle := verifySpill(t, rawDB, runID, mem.Name())
	if firstHandle == secondHandle {
		t.Fatalf("expected new handle on overwrite; both = %q", firstHandle)
	}

	// @constraint: overwriting a spilled row enqueues the prior handle into rimsky_blob_orphans.
	orphRows, err := orphans.DueBefore(ctx, time.Now().Add(48*time.Hour), 100)
	if err != nil {
		t.Fatalf("orphans.DueBefore: %v", err)
	}
	foundFirst := false
	for _, r := range orphRows {
		if r.Handle == firstHandle {
			foundFirst = true
			if r.Backend != mem.Name() {
				t.Fatalf("orphan row backend: got %q, want %q", r.Backend, mem.Name())
			}
		}
	}
	if !foundFirst {
		t.Fatalf("first handle %q not found in orphans (got %d rows)", firstHandle, len(orphRows))
	}

	// @constraint: downgrading a spilled row to inline clears value_handle and queues the prior handle as an orphan.
	tiny := map[string]any{"k": "v"}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return attrs.Upsert(ctx, runID, nodeID, tiny, tx)
	}); err != nil {
		t.Fatalf("Upsert tiny: %v", err)
	}
	verifyNoSpill(t, rawDB, runID)

	orphRows, err = orphans.DueBefore(ctx, time.Now().Add(48*time.Hour), 100)
	if err != nil {
		t.Fatalf("orphans.DueBefore (post-downgrade): %v", err)
	}
	foundSecond := false
	for _, r := range orphRows {
		if r.Handle == secondHandle {
			foundSecond = true
		}
	}
	if !foundSecond {
		t.Fatalf("second handle not found in orphans on downgrade (got %d rows)", len(orphRows))
	}
}

// TestNodeAttributesMergeDeltaSpill exercises the spill-aware MergeDelta
// path: starting from a spilled row, MergeDelta materializes, merges,
// and re-spills (or downgrades to inline).
func TestNodeAttributesMergeDeltaSpill(t *testing.T) {
	t.Setenv(persistence.ProcessRoleEnv, "unified")
	d := openSQLite(t)
	ctx := context.Background()
	rawDB := sqlitedrv.DBFromDatabase(d)

	mem := persistence.NewMemoryBackend()
	d.SetBlobBackend(mem, 256, time.Hour)

	store := d.Tables()
	attrs := store.NodeAttributes()
	nodeID, runID := seedFixtureNodeAndRun(t, rawDB)

	bigVal := strings.Repeat("y", 500)
	initial := map[string]any{"big": bigVal, "phase": "a"}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return attrs.Upsert(ctx, runID, nodeID, initial, tx)
	}); err != nil {
		t.Fatalf("Upsert initial: %v", err)
	}
	_ = verifySpill(t, rawDB, runID, mem.Name())

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return attrs.MergeDelta(ctx, runID, map[string]any{"phase": "b"}, tx)
	}); err != nil {
		t.Fatalf("MergeDelta: %v", err)
	}
	got := readData(t, store, runID)
	if got["big"] != bigVal {
		t.Fatalf("MergeDelta: big lost")
	}
	if got["phase"] != "b" {
		t.Fatalf("MergeDelta: phase=%v, want b", got["phase"])
	}
	_ = verifySpill(t, rawDB, runID, mem.Name())
}

// verifyNoSpill asserts the row's value_handle is NULL.
func verifyNoSpill(t *testing.T, rawDB *sql.DB, runID uuid.UUID) {
	t.Helper()
	h, _ := readSpillHandle(t, rawDB, runID)
	if h != "" {
		t.Fatalf("expected no spill handle for %s, got %q", runID, h)
	}
}

// verifySpill asserts the row's value_handle is non-empty and its
// backend matches. Returns the handle.
func verifySpill(t *testing.T, rawDB *sql.DB, runID uuid.UUID, wantBackend string) string {
	t.Helper()
	h, b := readSpillHandle(t, rawDB, runID)
	if h == "" {
		t.Fatalf("expected spill handle for %s, got empty", runID)
	}
	if b != wantBackend {
		t.Fatalf("expected spill backend %q, got %q", wantBackend, b)
	}
	return h
}

// seedFixtureNodeAndRun inserts the FK chain (template → instance →
// frame → node → node_run) so a rimsky_node_attributes row (post per-run
// keying) is FK-valid. Returns (nodeID, runID).
func seedFixtureNodeAndRun(t *testing.T, rawDB *sql.DB) (uuid.UUID, uuid.UUID) {
	t.Helper()
	templateID := "sha256-" + uuid.NewString()
	instanceID := uuid.New().String()
	nodeID := uuid.New()
	frameID := uuid.New().String()
	runID := uuid.New()
	ctx := context.Background()
	_, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_templates (id, spec, state, source) VALUES (?, '{}', 'registered', 'direct')`,
		templateID,
	)
	if err != nil {
		t.Fatalf("seed template: %v", err)
	}
	// @constraint: rimsky_instances and rimsky_run_scopes mutually FK each other (DEFERRABLE INITIALLY DEFERRED) so both must be seeded in the same transaction; rimsky_node_runs also requires a run_scope_id below.
	scopeID := uuid.New().String()
	stx, err := rawDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = stx.Rollback() }()
	if _, err = stx.ExecContext(ctx,
		`INSERT INTO rimsky_instances (id, template_hash, main_run_scope_id) VALUES (?, ?, ?)`,
		instanceID, templateID, scopeID,
	); err != nil {
		t.Fatalf("seed instance: %v", err)
	}
	if _, err = stx.ExecContext(ctx,
		`INSERT INTO rimsky_run_scopes (id, graph_name, partition_key, instance_id) VALUES (?, 'main', '', ?)`,
		scopeID, instanceID,
	); err != nil {
		t.Fatalf("seed run_scope: %v", err)
	}
	if err := stx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	_, err = rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_nodes (id, instance_id, node_type) VALUES (?, ?, 'fixture')`,
		nodeID.String(), instanceID,
	)
	if err != nil {
		t.Fatalf("seed node: %v", err)
	}
	// @constraint: rimsky_frames.triggering_message_id is a NOT NULL FK to rimsky_messages(id); seed the triggering message before the frame insert below.
	msgID := uuid.New().String()
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_messages (id, instance_id, type, sender, sender_kind)
		 VALUES (?, ?, 'fixture/message', 'operator', 'operator')`,
		msgID, instanceID,
	); err != nil {
		t.Fatalf("seed message: %v", err)
	}
	// @constraint: rimsky_node_runs.frame_id FK requires a frame row; seed a running frame so the run insert below succeeds.
	_, err = rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_frames
		   (frame_id, instance_id, triggering_message_id, state,
		    queued_at, started_at, frame_timeout_ms)
		 VALUES (?, ?, ?, 'running',
		         datetime('now'), datetime('now'), 600000)`,
		frameID, instanceID, msgID,
	)
	if err != nil {
		t.Fatalf("seed frame: %v", err)
	}
	// @constraint: rimsky_node_attributes.node_run_id FK requires a node-run row; seed a pending run so per-run-keyed attribute writes succeed.
	_, err = rawDB.ExecContext(ctx,
		`INSERT INTO rimsky_node_runs
		   (id, node_id, executor_name, required_stores, enqueued_at, phase, state, frame_id, run_scope_id)
		 VALUES (?, ?, 'stub', '[]', datetime('now'), 'pending', 'stale', ?, ?)`,
		runID.String(), nodeID.String(), frameID, scopeID,
	)
	if err != nil {
		t.Fatalf("seed run: %v", err)
	}
	return nodeID, runID
}

// readSpillHandle is a SQL escape hatch — the public NodeAttributeTable
// API does not surface the value_handle/value_handle_backend columns
// directly (the read path dereferences them transparently). The test
// peeks at the row via the test-only DBFromDatabase accessor.
func readSpillHandle(t *testing.T, rawDB *sql.DB, runID uuid.UUID) (string, string) {
	t.Helper()
	row := rawDB.QueryRowContext(context.Background(),
		`SELECT value_handle, value_handle_backend FROM rimsky_node_attributes WHERE node_run_id = ?`,
		runID.String(),
	)
	var h, b sql.NullString
	if err := row.Scan(&h, &b); err != nil {
		if err == sql.ErrNoRows {
			return "", ""
		}
		t.Fatalf("readSpillHandle scan: %v", err)
	}
	hs := ""
	bs := ""
	if h.Valid {
		hs = h.String
	}
	if b.Valid {
		bs = b.String
	}
	return hs, bs
}

// readData returns the materialized data map for runID.
func readData(t *testing.T, store persistence.Tables, runID uuid.UUID) map[string]any {
	t.Helper()
	var out *persistence.NodeAttributesRow
	if err := store.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		r, err := store.NodeAttributes().GetByRun(ctx, runID, tx)
		out = r
		return err
	}); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if out == nil {
		t.Fatalf("Get returned nil")
	}
	return out.Data
}
