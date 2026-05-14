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

	"github.com/fallguy/rimsky/foundation/persistence"
	sqlitedrv "github.com/fallguy/rimsky/foundation/persistence/sqlite"
)

// TestNodeAttributesSpillRoundtrip exercises D6/D7 wiring against the
// SQLite driver + the in-memory BlobBackend:
//   - small payload (below threshold) stored inline; value_handle is NULL.
//   - large payload (above threshold) spilled; value_handle non-NULL,
//     `data` reset to '{}', read transparently dereferences.
//   - overwriting a spilled row inserts the prior handle into
//     rimsky_blob_orphans.
//   - downgrading from spilled to inline clears value_handle and queues
//     the prior handle as an orphan.
//   - MergeDelta against a spilled row materializes, merges, re-spills.
func TestNodeAttributesSpillRoundtrip(t *testing.T) {
	t.Setenv(persistence.ProcessRoleEnv, "unified")
	d := openSQLite(t)
	ctx := context.Background()
	rawDB := sqlitedrv.DBFromDatabase(d)

	mem := persistence.NewMemoryBackend()
	d.SetBlobBackend(mem, 256, time.Hour) // spill above 256 bytes

	store := d.Tables()
	attrs := store.NodeAttributes()
	orphans := store.BlobOrphans()

	nodeID := seedFixtureNode(t, rawDB)

	// Small payload — should go inline (≤ 256 bytes after marshal).
	small := map[string]any{"k": "v"}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return attrs.Upsert(ctx, nodeID, 1, small, tx)
	}); err != nil {
		t.Fatalf("Upsert small: %v", err)
	}
	verifyNoSpill(t, rawDB, nodeID)

	got := readData(t, store, nodeID)
	if got["k"] != "v" {
		t.Fatalf("Get small: got %v, want k=v", got)
	}

	// Large payload — should spill.
	bigVal := strings.Repeat("x", 500)
	large := map[string]any{"big": bigVal, "tag": "first"}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return attrs.Upsert(ctx, nodeID, 2, large, tx)
	}); err != nil {
		t.Fatalf("Upsert large: %v", err)
	}
	firstHandle := verifySpill(t, rawDB, nodeID, mem.Name())

	// Read returns the materialized data via the backend.
	got = readData(t, store, nodeID)
	if got["big"] != bigVal {
		t.Fatalf("Get large: big mismatch")
	}
	if got["tag"] != "first" {
		t.Fatalf("Get large: tag=%v, want first", got["tag"])
	}

	// Overwrite the spilled row with another large payload.
	large2 := map[string]any{"big": bigVal, "tag": "second"}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return attrs.Upsert(ctx, nodeID, 3, large2, tx)
	}); err != nil {
		t.Fatalf("Upsert large2: %v", err)
	}
	secondHandle := verifySpill(t, rawDB, nodeID, mem.Name())
	if firstHandle == secondHandle {
		t.Fatalf("expected new handle on overwrite; both = %q", firstHandle)
	}

	// The first handle should now be queued as an orphan.
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

	// Downgrade: replace with a small payload. value_handle should clear,
	// secondHandle should queue as orphan.
	tiny := map[string]any{"k": "v"}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return attrs.Upsert(ctx, nodeID, 4, tiny, tx)
	}); err != nil {
		t.Fatalf("Upsert tiny: %v", err)
	}
	verifyNoSpill(t, rawDB, nodeID)

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
	nodeID := seedFixtureNode(t, rawDB)

	bigVal := strings.Repeat("y", 500)
	initial := map[string]any{"big": bigVal, "phase": "a"}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return attrs.Upsert(ctx, nodeID, 1, initial, tx)
	}); err != nil {
		t.Fatalf("Upsert initial: %v", err)
	}
	_ = verifySpill(t, rawDB, nodeID, mem.Name())

	// Merge a delta in. The result is still big → still spilled.
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return attrs.MergeDelta(ctx, nodeID, map[string]any{"phase": "b"}, tx)
	}); err != nil {
		t.Fatalf("MergeDelta: %v", err)
	}
	got := readData(t, store, nodeID)
	if got["big"] != bigVal {
		t.Fatalf("MergeDelta: big lost")
	}
	if got["phase"] != "b" {
		t.Fatalf("MergeDelta: phase=%v, want b", got["phase"])
	}
	_ = verifySpill(t, rawDB, nodeID, mem.Name())
}

// verifyNoSpill asserts the row's value_handle is NULL.
func verifyNoSpill(t *testing.T, rawDB *sql.DB, nodeID uuid.UUID) {
	t.Helper()
	h, _ := readSpillHandle(t, rawDB, nodeID)
	if h != "" {
		t.Fatalf("expected no spill handle for %s, got %q", nodeID, h)
	}
}

// verifySpill asserts the row's value_handle is non-empty and its
// backend matches. Returns the handle.
func verifySpill(t *testing.T, rawDB *sql.DB, nodeID uuid.UUID, wantBackend string) string {
	t.Helper()
	h, b := readSpillHandle(t, rawDB, nodeID)
	if h == "" {
		t.Fatalf("expected spill handle for %s, got empty", nodeID)
	}
	if b != wantBackend {
		t.Fatalf("expected spill backend %q, got %q", wantBackend, b)
	}
	return h
}

// seedFixtureNode inserts the FK chain (template → instance → node) so a
// rimsky_node_attributes row is FK-valid. Returns the inserted node id.
func seedFixtureNode(t *testing.T, rawDB *sql.DB) uuid.UUID {
	t.Helper()
	templateID := "sha256-" + uuid.NewString()
	instanceID := uuid.New().String()
	nodeID := uuid.New()
	_, err := rawDB.ExecContext(context.Background(),
		`INSERT INTO rimsky_templates (id, spec, state, source) VALUES (?, '{}', 'registered', 'direct')`,
		templateID,
	)
	if err != nil {
		t.Fatalf("seed template: %v", err)
	}
	_, err = rawDB.ExecContext(context.Background(),
		`INSERT INTO rimsky_instances (id, template_hash) VALUES (?, ?)`,
		instanceID, templateID,
	)
	if err != nil {
		t.Fatalf("seed instance: %v", err)
	}
	_, err = rawDB.ExecContext(context.Background(),
		`INSERT INTO rimsky_nodes (id, instance_id, node_type, state) VALUES (?, ?, 'fixture', 'fresh')`,
		nodeID.String(), instanceID,
	)
	if err != nil {
		t.Fatalf("seed node: %v", err)
	}
	return nodeID
}

// readSpillHandle is a SQL escape hatch — the public NodeAttributeTable
// API does not surface the value_handle/value_handle_backend columns
// directly (the read path dereferences them transparently). The test
// peeks at the row via the test-only DBFromDatabase accessor.
func readSpillHandle(t *testing.T, rawDB *sql.DB, nodeID uuid.UUID) (string, string) {
	t.Helper()
	row := rawDB.QueryRowContext(context.Background(),
		`SELECT value_handle, value_handle_backend FROM rimsky_node_attributes WHERE node_id = ?`,
		nodeID.String(),
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

// readData returns the materialized data map for nodeID.
func readData(t *testing.T, store persistence.Tables, nodeID uuid.UUID) map[string]any {
	t.Helper()
	var out *persistence.NodeAttributesRow
	if err := store.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		r, err := store.NodeAttributes().Get(ctx, nodeID, tx)
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
