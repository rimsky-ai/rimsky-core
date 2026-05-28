// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// schema_consolidation_test.go — sqlite analog of the postgres
// schema-consolidation test per spec §10.5. The sqlite_master / pragma
// table_info introspection replaces pg_catalog; everything else is
// the same shape (fresh-DB schema match + stale-rimsky_migrations-row
// inert behavior).
//
// @concept: breakpoint

package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	sqlitepersist "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// expectedSqliteTables is the post-consolidation table set on SQLite.
// The list mirrors the postgres-side expectedTables in the sibling
// schema_consolidation_test.go (the rimsky_* table set is the same on
// both backends).
var expectedSqliteTables = []string{
	"rimsky_api_keys",
	"rimsky_blob_orphans",
	"rimsky_breakpoint_hits",
	"rimsky_claim_handles",
	"rimsky_claim_holders",
	"rimsky_events",
	"rimsky_frames",
	"rimsky_instance_breakpoints",
	"rimsky_instances",
	"rimsky_lifecycle_idempotencies",
	"rimsky_lineage",
	"rimsky_message_idempotencies",
	"rimsky_messages",
	"rimsky_migrations",
	"rimsky_node_attributes",
	"rimsky_node_events",
	"rimsky_node_runs",
	"rimsky_nodes",
	"rimsky_publisher_subscriptions",
	"rimsky_run_scopes",
	"rimsky_supervisors",
	"rimsky_template_tags",
	"rimsky_templates",
	"rimsky_wait_set",
}

var expectedSqliteBreakpointColumns = []string{
	"id", "instance_id", "matcher", "checkpoint", "signal_type", "mode",
	"overflow_policy", "hit_ttl_seconds", "ttl_seconds", "dropped_count",
	"created_by_key", "created_at", "expires_at",
}

var expectedSqliteHitColumns = []string{
	"seq", "id", "breakpoint_id", "instance_id", "node_run_id", "frame_id",
	"checkpoint", "mode", "snapshot", "hit_at", "resumed_at", "resumed_by_key",
	"resume_overlay",
}

// TestSqliteSchemaConsolidation_FreshDB asserts that after Migrate, the
// expected tables exist and the breakpoint tables carry their full
// column sets.
func TestSqliteSchemaConsolidation_FreshDB(t *testing.T) {
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
	assertSqliteTablesPresent(t, ctx, db, expectedSqliteTables)
	assertSqliteColumnsPresent(t, ctx, db, "rimsky_instance_breakpoints", expectedSqliteBreakpointColumns)
	assertSqliteColumnsPresent(t, ctx, db, "rimsky_breakpoint_hits", expectedSqliteHitColumns)
	assertSqliteColumnsPresent(t, ctx, db, "rimsky_instances", []string{"paused"})
}

// TestSqliteSchemaConsolidation_StaleMigrationsRowsAreInert mirrors the
// postgres test: pre-seed rimsky_migrations with the legacy filenames,
// then Migrate. The new 001-schema.sql is not in the seed set so it
// applies; the legacy rows are ignored because their filenames are not
// in the embed.FS.
func TestSqliteSchemaConsolidation_StaleMigrationsRowsAreInert(t *testing.T) {
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

	db := sqlitepersist.DBFromDatabase(d)

	// Pre-seed rimsky_migrations with the legacy filenames.
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS rimsky_migrations (
		filename    TEXT PRIMARY KEY,
		applied_at  TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		t.Fatalf("seed rimsky_migrations table: %v", err)
	}
	stale := []string{
		"001-baseline.sql",
		"002-tags.sql",
		"003-some-other.sql",
		"004-more.sql",
		"005-iterations.sql",
		"006-of.sql",
		"007-the.sql",
		"008-legacy.sql",
		"009-migration.sql",
		"010-series.sql",
		"011-that.sql",
		"012-no.sql",
		"013-longer.sql",
		"014-drop-last-outcome.sql",
	}
	for _, name := range stale {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO rimsky_migrations (filename) VALUES (?) ON CONFLICT DO NOTHING`,
			name); err != nil {
			t.Fatalf("seed stale row %s: %v", name, err)
		}
	}

	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate with stale rows: %v", err)
	}

	assertSqliteTablesPresent(t, ctx, db, expectedSqliteTables)
	assertSqliteColumnsPresent(t, ctx, db, "rimsky_instance_breakpoints", expectedSqliteBreakpointColumns)
	assertSqliteColumnsPresent(t, ctx, db, "rimsky_breakpoint_hits", expectedSqliteHitColumns)

	// rimsky_migrations should carry BOTH the legacy row AND the new
	// 001-schema.sql row.
	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rimsky_migrations WHERE filename = '001-schema.sql'`,
	).Scan(&n); err != nil {
		t.Fatalf("query 001-schema.sql row: %v", err)
	}
	if n != 1 {
		t.Errorf("rimsky_migrations should record 001-schema.sql exactly once; got %d", n)
	}
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rimsky_migrations WHERE filename = '001-baseline.sql'`,
	).Scan(&n); err != nil {
		t.Fatalf("query 001-baseline.sql row: %v", err)
	}
	if n != 1 {
		t.Errorf("rimsky_migrations should still carry the legacy 001-baseline.sql row (inert); got %d", n)
	}
}

func assertSqliteTablesPresent(t *testing.T, ctx context.Context, db *sql.DB, want []string) {
	t.Helper()
	have := map[string]bool{}
	rows, err := db.QueryContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		have[name] = true
	}
	for _, name := range want {
		if !have[name] {
			t.Errorf("expected table %q missing from schema", name)
		}
	}
}

func assertSqliteColumnsPresent(t *testing.T, ctx context.Context, db *sql.DB, table string, want []string) {
	t.Helper()
	have := map[string]bool{}
	// PRAGMA can't be parameterized — concatenate the table name. The
	// `table` value comes from the test's static lists, not user input.
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		t.Fatalf("pragma table_info(%s): %v", table, err)
	}
	defer rows.Close()
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
			t.Fatalf("scan column for %s: %v", table, err)
		}
		have[name] = true
	}
	for _, name := range want {
		if !have[name] {
			t.Errorf("expected column %s.%s missing", table, name)
		}
	}
}
