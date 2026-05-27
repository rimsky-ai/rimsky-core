// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rimsky-ai/rimsky-core/foundation/persistence"
	sqlitepersist "github.com/rimsky-ai/rimsky-core/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/foundation/shared"
)

// TestSQLiteMigrationApplies verifies that the embedded init.sql applies
// cleanly against a fresh SQLite database and that re-running Migrate
// is a no-op (idempotent).
func TestSQLiteMigrationApplies(t *testing.T) {
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
	// Idempotent re-run.
	if err := d.Migrate(context.Background(), shared.SilentLogger{}); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}
}

// TestSQLiteMigration002Tags pins migration 002's contract per spec
// 2026-05-19-multi-instance-template-ergonomics-design.md §Item 4:
//
//   - Column `tags` exists on `rimsky_nodes` as `TEXT NOT NULL` with
//     default `'[]'` (sibling JSON-encoded-array convention).
//   - A row inserted WITHOUT setting `tags` materializes the default
//     `'[]'` (matching the sibling `accepted_stores` / `required_stores`
//     pattern).
//
// SQLite has no GIN equivalent, so the postgres-side GIN index check
// has no sqlite analog.
func TestSQLiteMigration002Tags(t *testing.T) {
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

	// 1. Column exists with the right SQLite type + NOT NULL flag +
	//    default. SQLite's table_info pragma reports per-column metadata.
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

	// 2. A row inserted without setting `tags` materializes `'[]'`.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO rimsky_templates (id, spec, state)
		 VALUES ('tpl-1', '{}', 'deployed')`); err != nil {
		t.Fatalf("seed template: %v", err)
	}
	// Post-RunScope-first: instance + main_run_scope mutually FK each
	// other (DEFERRABLE INITIALLY DEFERRED). Seed in one tx.
	stx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = stx.Rollback() }()
	if _, err := stx.ExecContext(ctx,
		`INSERT INTO rimsky_instances (id, template_hash, instance_key, main_run_scope_id)
		 VALUES ('inst-1', 'tpl-1', 'ck-1', 'scope-1')`); err != nil {
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
