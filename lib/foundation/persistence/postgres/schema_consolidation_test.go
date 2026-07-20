// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: breakpoint

package postgres_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/internal/pgtest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	pgpersist "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/postgres"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

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
	"rimsky_node_runs",
	"rimsky_producer_verb_outbox",
	"rimsky_nodes",
	"rimsky_publisher_subscriptions",
	"rimsky_run_scopes",
	"rimsky_supervisors",
	"rimsky_template_tags",
	"rimsky_templates",
	"rimsky_wait_set",
}

var expectedBreakpointColumns = []string{
	"id", "instance_id", "matcher", "checkpoint", "signal_type", "mode",
	"overflow_policy", "hit_ttl_seconds", "ttl_seconds", "dropped_count",
	"created_by_key", "created_at", "expires_at",
}

var expectedHitColumns = []string{
	"seq", "id", "breakpoint_id", "instance_id", "node_run_id", "frame_id",
	"checkpoint", "mode", "snapshot", "hit_at", "resumed_at", "resumed_by_key",
	"resume_overlay",
}

// @concept: run-scope
var expectedRunScopeColumns = []string{
	"id", "parent_run_scope_id", "parent_run_id", "graph_name",
	"partition_key", "instance_id", "created_at", "closed_at",
}

// @concept: inertness
var executorNamedPersistenceSurfaces = []string{
	"rimsky_pending_timer",
}

func TestSchemaConsolidation_FreshDBSchemaShape(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dsn := pgtest.StartUnmigratedPostgresDSN(ctx, t)

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
	assertTablesAbsent(t, ctx, pool, executorNamedPersistenceSurfaces)
}

// @concept: run-scope
func TestRunScopesColumnSet_HasNoStoredKindColumn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dsn := pgtest.StartUnmigratedPostgresDSN(ctx, t)

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

	assertColumnsExact(t, ctx, pool, "rimsky_run_scopes", expectedRunScopeColumns)
}

func TestSchemaConsolidation_StaleMigrationsRowsAreInert(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dsn := pgtest.StartUnmigratedPostgresDSN(ctx, t)

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

	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS rimsky_migrations (
		filename    TEXT PRIMARY KEY,
		applied_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`); err != nil {
		t.Fatalf("seed rimsky_migrations: %v", err)
	}
	stale := []string{
		"099-some-future-rollback.sql",
		"100-experimental.sql",
	}
	for _, name := range stale {
		if _, err := pool.Exec(ctx,
			`INSERT INTO rimsky_migrations (filename) VALUES ($1) ON CONFLICT DO NOTHING`,
			name); err != nil {
			t.Fatalf("seed stale row %s: %v", name, err)
		}
	}

	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate with stale rows: %v", err)
	}

	assertTablesPresent(t, ctx, pool, expectedTables)
	assertColumnsPresent(t, ctx, pool, "rimsky_instance_breakpoints", expectedBreakpointColumns)
	assertColumnsPresent(t, ctx, pool, "rimsky_breakpoint_hits", expectedHitColumns)

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM rimsky_migrations WHERE filename = '001-initial.sql'`,
	).Scan(&count); err != nil {
		t.Fatalf("query 001-initial.sql row: %v", err)
	}
	if count != 1 {
		t.Errorf("rimsky_migrations should record 001-initial.sql exactly once; got %d", count)
	}
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM rimsky_migrations WHERE filename = '099-some-future-rollback.sql'`,
	).Scan(&count); err != nil {
		t.Fatalf("query unknown-row presence: %v", err)
	}
	if count != 1 {
		t.Errorf("rimsky_migrations should preserve unknown filenames inertly; got %d", count)
	}
}

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

func assertTablesAbsent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, forbidden []string) {
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
	for _, name := range forbidden {
		if have[name] {
			t.Errorf("table %q must not exist — the persistence layer must not know executor "+
				"specifics; any executor state surface is generic and exposed through the protocol", name)
		}
	}
}

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

func assertColumnsExact(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table string, want []string) {
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
	wantSet := map[string]bool{}
	for _, name := range want {
		wantSet[name] = true
		if !have[name] {
			t.Errorf("expected column %s.%s missing", table, name)
		}
	}
	for name := range have {
		if !wantSet[name] {
			t.Errorf("unexpected column %s.%s present; %s must expose only structural fields, never a derived/stored classification column",
				table, name, table)
		}
	}
}
