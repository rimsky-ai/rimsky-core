// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// schema_consolidation_test.go — pins the spec §10.5 consolidation
// contract: the embedded 001-schema.sql produces an end-state schema
// matching the prior multi-migration series, AND tolerates a stale
// rimsky_migrations table left over from an aborted prior series
// (001-baseline.sql through 014-drop-last-outcome.sql).
//
// @concept: breakpoint
//
// The first test (TestSchemaConsolidation_FreshDBSchemaShape) introspects
// pg_catalog to confirm that every table the codebase references is
// present after a clean migrate. The list is the union of every
// rimsky_* table referenced by the persistence layer.
//
// The second test (TestSchemaConsolidation_StaleMigrationsRowsAreInert)
// pre-seeds rimsky_migrations with the legacy filenames before running
// Migrate. The migrator's name-set is determined by the embed.FS
// contents (which only carries 001-schema.sql), so the stale rows are
// ignored. 001-schema.sql still applies cleanly because the bootstrap
// CREATE TABLE IF NOT EXISTS does NOT error on an existing
// rimsky_migrations row set.

package postgres_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rimsky-ai/rimsky-core/foundation/internal/pgtest"
	"github.com/rimsky-ai/rimsky-core/foundation/persistence"
	pgpersist "github.com/rimsky-ai/rimsky-core/foundation/persistence/postgres"
	"github.com/rimsky-ai/rimsky-core/foundation/shared"
)

// expectedTables is the post-consolidation table set. Every rimsky_*
// table the persistence layer reads or writes. If a future migration
// adds or renames a table, this list MUST be updated alongside it.
var expectedTables = []string{
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

// expectedBreakpointColumns is the column set for rimsky_instance_breakpoints
// per the spec §7.2 schema. Pinned here so a future schema change is
// caught at test time.
var expectedBreakpointColumns = []string{
	"id", "instance_id", "matcher", "checkpoint", "signal_type", "mode",
	"overflow_policy", "hit_ttl_seconds", "ttl_seconds", "dropped_count",
	"created_by_key", "created_at", "expires_at",
}

// expectedHitColumns is the column set for rimsky_breakpoint_hits.
var expectedHitColumns = []string{
	"seq", "id", "breakpoint_id", "instance_id", "node_run_id", "frame_id",
	"checkpoint", "mode", "snapshot", "hit_at", "resumed_at", "resumed_by_key",
	"resume_overlay",
}

// TestSchemaConsolidation_FreshDBSchemaShape brings up a fresh database,
// applies migrations, and asserts that the introspected schema matches
// the expected post-consolidation shape: every expected table present,
// the breakpoint tables carry their full column sets, and the
// rimsky_instances.paused column exists.
func TestSchemaConsolidation_FreshDBSchemaShape(t *testing.T) {
	ctx := context.Background()
	dsn, terminate := pgtest.StartFreshPostgresDSN(ctx, t)
	t.Cleanup(terminate)

	d, err := persistence.Open(ctx, persistence.Config{
		Driver:   "postgres",
		Postgres: &persistence.PostgresConfig{DSN: dsn},
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	pool, ok := pgpersist.PoolFromDatabaseForTest(d)
	if !ok {
		t.Fatalf("PoolFromDatabaseForTest: not a postgres driver")
	}

	assertTablesPresent(t, ctx, pool, expectedTables)
	assertColumnsPresent(t, ctx, pool, "rimsky_instance_breakpoints", expectedBreakpointColumns)
	assertColumnsPresent(t, ctx, pool, "rimsky_breakpoint_hits", expectedHitColumns)
	assertColumnsPresent(t, ctx, pool, "rimsky_instances", []string{"paused"})
}

// TestSchemaConsolidation_StaleMigrationsRowsAreInert simulates the
// "ephemeral CI environment with leftover rimsky_migrations state"
// case from spec §10.5: pre-seed rimsky_migrations with the legacy
// filenames, then run Migrate. The expected outcome is that
// 001-schema.sql applies (because its filename is NOT in the pre-seed
// set) and the resulting schema is identical to the fresh-DB case.
func TestSchemaConsolidation_StaleMigrationsRowsAreInert(t *testing.T) {
	ctx := context.Background()
	dsn, terminate := pgtest.StartFreshPostgresDSN(ctx, t)
	t.Cleanup(terminate)

	d, err := persistence.Open(ctx, persistence.Config{
		Driver:   "postgres",
		Postgres: &persistence.PostgresConfig{DSN: dsn},
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	pool, ok := pgpersist.PoolFromDatabaseForTest(d)
	if !ok {
		t.Fatalf("PoolFromDatabaseForTest: not a postgres driver")
	}

	// Pre-seed rimsky_migrations with the legacy filenames. We have to
	// bootstrap the table ourselves first because Migrate's Bootstrap
	// runs the same CREATE TABLE IF NOT EXISTS — the test's seed
	// creates the table, the migrator no-ops the create, then the
	// migrator's QueryHas check sees the seeded rows for the LEGACY
	// names (none of which are on disk) and the actual on-disk
	// 001-schema.sql is unseeded so it runs.
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS rimsky_migrations (
		filename    TEXT PRIMARY KEY,
		applied_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`); err != nil {
		t.Fatalf("seed rimsky_migrations: %v", err)
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
		if _, err := pool.Exec(ctx,
			`INSERT INTO rimsky_migrations (filename) VALUES ($1) ON CONFLICT DO NOTHING`,
			name); err != nil {
			t.Fatalf("seed stale row %s: %v", name, err)
		}
	}

	// Now run migrations. 001-schema.sql is the only file in the embed
	// FS; its filename is NOT in the pre-seed set, so it must apply.
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate with stale rows: %v", err)
	}

	// Verify the consolidated schema is in place — same as the fresh-DB
	// test.
	assertTablesPresent(t, ctx, pool, expectedTables)
	assertColumnsPresent(t, ctx, pool, "rimsky_instance_breakpoints", expectedBreakpointColumns)
	assertColumnsPresent(t, ctx, pool, "rimsky_breakpoint_hits", expectedHitColumns)

	// Confirm rimsky_migrations carries BOTH the stale rows AND the
	// new 001-schema.sql row.
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM rimsky_migrations WHERE filename = '001-schema.sql'`,
	).Scan(&count); err != nil {
		t.Fatalf("query 001-schema.sql row: %v", err)
	}
	if count != 1 {
		t.Errorf("rimsky_migrations should record 001-schema.sql exactly once; got %d", count)
	}
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM rimsky_migrations WHERE filename = '001-baseline.sql'`,
	).Scan(&count); err != nil {
		t.Fatalf("query 001-baseline.sql row: %v", err)
	}
	if count != 1 {
		t.Errorf("rimsky_migrations should still carry the legacy 001-baseline.sql row (inert); got %d", count)
	}
}

// assertTablesPresent fatals if any expected table is missing from the
// current schema.
func assertTablesPresent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, want []string) {
	t.Helper()
	have := map[string]bool{}
	rows, err := pool.Query(ctx, `
		SELECT table_name FROM information_schema.tables
		 WHERE table_schema = current_schema()
		   AND table_type = 'BASE TABLE'
	`)
	if err != nil {
		t.Fatalf("query tables: %v", err)
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

// assertColumnsPresent fatals if any expected column is missing from
// the given table.
func assertColumnsPresent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table string, want []string) {
	t.Helper()
	have := map[string]bool{}
	rows, err := pool.Query(ctx, `
		SELECT column_name FROM information_schema.columns
		 WHERE table_schema = current_schema()
		   AND table_name = $1
	`, table)
	if err != nil {
		t.Fatalf("query columns for %s: %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
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
