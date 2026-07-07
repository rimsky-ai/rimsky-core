// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	sqlitepersist "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func TestSQLiteMigrationApplies(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	d, err := persistence.Open(context.Background(), persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(dir, "state.db")},
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	if err := d.Migrate(context.Background(), shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := d.Migrate(context.Background(), shared.SilentLogger{}); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}
}

func TestSQLiteMigration021RebuildPreservesChildRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(dir, "state.db")},
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	db := sqlitepersist.DBFromDatabase(d)
	seed := []string{
		`INSERT INTO rimsky_templates (id, spec, state) VALUES ('tpl-021', '{}', 'deployed')`,
		`INSERT INTO rimsky_instances (id, template_hash, instance_key) VALUES ('inst-021', 'tpl-021', 'ck-021')`,
		`INSERT INTO rimsky_run_scopes (id, graph_name, partition_key, instance_id)
		 VALUES ('scope-021', 'main', '', 'inst-021')`,
		`INSERT INTO rimsky_messages (id, instance_id, type, sender, sender_kind)
		 VALUES ('msg-021', 'inst-021', 'loop/wake', 'op', 'operator')`,
		`INSERT INTO rimsky_frames (frame_id, instance_id, triggering_message_id, root_run_scope_id,
		                            started_at, ended_at, frame_timeout_ms)
		 VALUES ('frame-021', 'inst-021', 'msg-021', 'scope-021', datetime('now'), datetime('now'), 60000)`,
		`INSERT INTO rimsky_nodes (id, instance_id, node_type) VALUES ('node-021', 'inst-021', 'n1')`,
		`INSERT INTO rimsky_node_runs (id, node_id, frame_id, sequence, run_scope_id)
		 VALUES ('run-021', 'node-021', 'frame-021', 1, 'scope-021')`,
	}
	for _, stmt := range seed {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed: %v\n%s", err, stmt)
		}
	}

	if _, err := db.ExecContext(ctx,
		`DELETE FROM rimsky_migrations WHERE filename = '021-retire-frame-state-column.sql'`); err != nil {
		t.Fatalf("unmark 021: %v", err)
	}
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("re-apply 021 against populated db: %v", err)
	}

	var frames, runs int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM rimsky_frames`).Scan(&frames); err != nil {
		t.Fatalf("count frames: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM rimsky_node_runs`).Scan(&runs); err != nil {
		t.Fatalf("count node_runs: %v", err)
	}
	if frames != 1 || runs != 1 {
		t.Fatalf("021 table rebuild lost rows: frames=%d node_runs=%d (want 1/1) — DROP TABLE must not cascade-delete children",
			frames, runs)
	}
	var runFrame string
	if err := db.QueryRowContext(ctx,
		`SELECT frame_id FROM rimsky_node_runs WHERE id = 'run-021'`).Scan(&runFrame); err != nil {
		t.Fatalf("read surviving node_run: %v", err)
	}
	if runFrame != "frame-021" {
		t.Fatalf("surviving node_run points at %q, want frame-021", runFrame)
	}
}

func TestSQLiteMigration002Tags(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(dir, "state.db")},
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	db := sqlitepersist.DBFromDatabase(d)

	rows, err := db.QueryContext(ctx, `PRAGMA table_info(rimsky_nodes)`)
	if err != nil {
		t.Fatalf("pragma table_info: %v", err)
	}
	defer rows.Close()
	var (
		foundTags    bool
		gotType      string
		gotNotNull   int
		gotDfltValue *string
	)
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    *string
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan column info: %v", err)
		}
		if name == "tags" {
			foundTags = true
			gotType = ctype
			gotNotNull = notnull
			gotDfltValue = dflt
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate columns: %v", err)
	}
	if !foundTags {
		t.Fatalf("tags column missing on rimsky_nodes")
	}
	if gotType != "TEXT" {
		t.Errorf("tags column type: got %q want TEXT", gotType)
	}
	if gotNotNull != 1 {
		t.Errorf("tags column NOT NULL flag: got %d want 1", gotNotNull)
	}
	if gotDfltValue == nil || *gotDfltValue != "'[]'" {
		got := "<nil>"
		if gotDfltValue != nil {
			got = *gotDfltValue
		}
		t.Errorf("tags column default: got %q want %q", got, "'[]'")
	}

	if _, err := db.ExecContext(ctx,
		`INSERT INTO rimsky_templates (id, spec, state)
		 VALUES ('tpl-1', '{}', 'deployed')`); err != nil {
		t.Fatalf("seed template: %v", err)
	}
	stx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = stx.Rollback() }()
	if _, err := stx.ExecContext(ctx,
		`INSERT INTO rimsky_instances (id, template_hash, instance_key)
		 VALUES ('inst-1', 'tpl-1', 'ck-1')`); err != nil {
		t.Fatalf("seed instance: %v", err)
	}
	if _, err := stx.ExecContext(ctx,
		`INSERT INTO rimsky_run_scopes (id, graph_name, partition_key, instance_id)
		 VALUES ('scope-1', 'main', '', 'inst-1')`); err != nil {
		t.Fatalf("seed run_scope: %v", err)
	}
	if err := stx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO rimsky_nodes (id, instance_id, node_type)
		 VALUES ('node-1', 'inst-1', 'n1')`); err != nil {
		t.Fatalf("seed node without tags: %v", err)
	}
	var tagsRaw string
	if err := db.QueryRowContext(ctx,
		`SELECT tags FROM rimsky_nodes WHERE id = 'node-1'`,
	).Scan(&tagsRaw); err != nil {
		t.Fatalf("read tags: %v", err)
	}
	if tagsRaw != "[]" {
		t.Errorf("tags default: got %q want %q", tagsRaw, "[]")
	}
}
