// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	sqlitepersist "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite/migrations"
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

func TestSQLiteMigration024RebuildPreservesChildRows(t *testing.T) {
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

	db, ok := sqlitepersist.DBFromDatabaseForTest(d)
	if !ok {
		t.Fatal("DBFromDatabaseForTest: not a sqlite database")
	}
	seed := []string{
		`INSERT INTO rimsky_templates (id, spec, state) VALUES ('tpl-024', '{}', 'deployed')`,
		`INSERT INTO rimsky_instances (id, template_hash, instance_key, target_routing_identity) VALUES ('inst-024', 'tpl-024', 'ck-024', 'test-daemon')`,
		`INSERT INTO rimsky_run_scopes (id, graph_name, partition_key, instance_id)
		 VALUES ('scope-024', 'main', '', 'inst-024')`,
		`INSERT INTO rimsky_messages (id, instance_id, type, sender, sender_kind)
		 VALUES ('msg-024', 'inst-024', 'loop/wake', 'op', 'operator')`,
		`INSERT INTO rimsky_frames (frame_id, instance_id, triggering_message_id, root_run_scope_id,
		                            started_at, ended_at)
		 VALUES ('frame-024', 'inst-024', 'msg-024', 'scope-024', datetime('now'), datetime('now'))`,
		`INSERT INTO rimsky_nodes (id, instance_id, node_type) VALUES ('node-024', 'inst-024', 'n1')`,
		`INSERT INTO rimsky_node_runs (id, node_id, frame_id, sequence, run_scope_id)
		 VALUES ('run-024', 'node-024', 'frame-024', 1, 'scope-024')`,
	}
	for _, stmt := range seed {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed: %v\n%s", err, stmt)
		}
	}

	sqlBytes, err := migrations.FS.ReadFile("024-retire-frame-timeout.sql")
	if err != nil {
		t.Fatalf("read 024 migration file: %v", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
		t.Fatalf("disable foreign_keys: %v", err)
	}
	if _, err := db.ExecContext(ctx, string(sqlBytes)); err != nil {
		t.Fatalf("re-apply 024 against populated db: %v", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys=ON"); err != nil {
		t.Fatalf("re-enable foreign_keys: %v", err)
	}

	var frames, runs int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM rimsky_frames`).Scan(&frames); err != nil {
		t.Fatalf("count frames: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM rimsky_node_runs`).Scan(&runs); err != nil {
		t.Fatalf("count node_runs: %v", err)
	}
	if frames != 1 || runs != 1 {
		t.Fatalf("024 table rebuild lost rows: frames=%d node_runs=%d (want 1/1) — DROP TABLE must not cascade-delete children",
			frames, runs)
	}
	var runFrame string
	if err := db.QueryRowContext(ctx,
		`SELECT frame_id FROM rimsky_node_runs WHERE id = 'run-024'`).Scan(&runFrame); err != nil {
		t.Fatalf("read surviving node_run: %v", err)
	}
	if runFrame != "frame-024" {
		t.Fatalf("surviving node_run points at %q, want frame-024", runFrame)
	}
}

func TestSQLiteMigration036RebuildPreservesEventsAndAutoincrement(t *testing.T) {
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

	db, ok := sqlitepersist.DBFromDatabaseForTest(d)
	if !ok {
		t.Fatal("DBFromDatabaseForTest: not a sqlite database")
	}
	seed := []string{
		`INSERT INTO rimsky_templates (id, spec, state) VALUES ('tpl-036', '{}', 'deployed')`,
		`INSERT INTO rimsky_instances (id, template_hash, instance_key, target_routing_identity) VALUES ('inst-036', 'tpl-036', 'ck-036', 'test-daemon')`,
		`INSERT INTO rimsky_events (id, instance_id, kind, payload, occurred_at)
		 VALUES (500, 'inst-036', 'fixture.kind', '{}', '2026-01-01T00:00:00.000000000Z')`,
	}
	for _, stmt := range seed {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed: %v\n%s", err, stmt)
		}
	}

	sqlBytes, err := migrations.FS.ReadFile("036-normalize-timestamp-column-dialect.sql")
	if err != nil {
		t.Fatalf("read 036 migration file: %v", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
		t.Fatalf("disable foreign_keys: %v", err)
	}
	if _, err := db.ExecContext(ctx, string(sqlBytes)); err != nil {
		t.Fatalf("re-apply 036 against populated db: %v", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys=ON"); err != nil {
		t.Fatalf("re-enable foreign_keys: %v", err)
	}

	var count int
	var occurredAt string
	if err := db.QueryRowContext(ctx,
		`SELECT count(*), occurred_at FROM rimsky_events WHERE id = 500 GROUP BY occurred_at`,
	).Scan(&count, &occurredAt); err != nil {
		t.Fatalf("read surviving event: %v", err)
	}
	if count != 1 {
		t.Fatalf("036 table rebuild lost or duplicated the seeded event row: count=%d", count)
	}
	if occurredAt != "2026-01-01T00:00:00.000000000Z" {
		t.Fatalf("036 table rebuild altered occurred_at: got %q", occurredAt)
	}

	res, err := db.ExecContext(ctx,
		`INSERT INTO rimsky_events (instance_id, kind, payload, occurred_at)
		 VALUES ('inst-036', 'fixture.kind2', '{}', '2026-01-01T00:00:01.000000000Z')`)
	if err != nil {
		t.Fatalf("insert post-rebuild event: %v", err)
	}
	newID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}
	if newID <= 500 {
		t.Fatalf("AUTOINCREMENT did not carry forward across the 036 rebuild: new id=%d, want >500 "+
			"(a reset here would let a post-migration event collide with a pre-migration id)", newID)
	}
}

func TestSQLiteMigrationTagsColumn(t *testing.T) {
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

	db, ok := sqlitepersist.DBFromDatabaseForTest(d)
	if !ok {
		t.Fatal("DBFromDatabaseForTest: not a sqlite database")
	}

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
		`INSERT INTO rimsky_instances (id, template_hash, instance_key, target_routing_identity)
		 VALUES ('inst-1', 'tpl-1', 'ck-1', 'test-daemon')`); err != nil {
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
