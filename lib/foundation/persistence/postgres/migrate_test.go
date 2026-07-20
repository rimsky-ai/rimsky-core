// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/internal/pgtest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	pgpersist "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/postgres"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func TestMigrateAgainstTestcontainers(t *testing.T) {
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
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}
}

func TestMigrationTagsColumn(t *testing.T) {
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

	pgPool, ok := pgpersist.PoolFromDatabaseForTest(d)
	if !ok {
		t.Fatalf("could not get *pgxpool.Pool from database")
	}

	var (
		colName   string
		dataType  string
		isNotNull string
		colDflt   *string
	)
	if err := pgPool.QueryRow(ctx, `
		SELECT column_name, data_type, is_nullable, column_default
		  FROM information_schema.columns
		 WHERE table_schema = current_schema()
		   AND table_name = 'rimsky_nodes'
		   AND column_name = 'tags'
	`).Scan(&colName, &dataType, &isNotNull, &colDflt); err != nil {
		t.Fatalf("query tags column: %v", err)
	}
	if dataType != "ARRAY" {
		t.Errorf("tags column data_type: got %q want %q", dataType, "ARRAY")
	}
	if isNotNull != "NO" {
		t.Errorf("tags column is_nullable: got %q want %q", isNotNull, "NO")
	}
	if colDflt == nil || *colDflt != "'{}'::text[]" {
		got := "<nil>"
		if colDflt != nil {
			got = *colDflt
		}
		t.Errorf("tags column default: got %q want %q", got, "'{}'::text[]")
	}

	if _, err := pgPool.Exec(ctx,
		`INSERT INTO rimsky_templates (id, spec, state)
		 VALUES ('tpl-1', '{}'::jsonb, 'deployed')`); err != nil {
		t.Fatalf("seed template: %v", err)
	}
	instUUID := uuid.New()
	scopeUUID := uuid.New()
	tx, err := pgPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET CONSTRAINTS ALL DEFERRED`); err != nil {
		t.Fatalf("defer constraints: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO rimsky_instances (id, template_hash, instance_key)
		 VALUES ($1, 'tpl-1', 'ck-1')`, instUUID); err != nil {
		t.Fatalf("seed instance: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO rimsky_run_scopes (id, graph_name, partition_key, instance_id)
		 VALUES ($1, 'main', '', $2)`, scopeUUID, instUUID); err != nil {
		t.Fatalf("seed run_scope: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	var instID string
	if err := pgPool.QueryRow(ctx,
		`SELECT id::text FROM rimsky_instances WHERE instance_key = 'ck-1'`,
	).Scan(&instID); err != nil {
		t.Fatalf("read instance id: %v", err)
	}
	if _, err := pgPool.Exec(ctx,
		`INSERT INTO rimsky_nodes (id, instance_id, node_type)
		 VALUES (gen_random_uuid(), $1::uuid, 'n1')`, instID); err != nil {
		t.Fatalf("seed node without tags: %v", err)
	}
	var tagsScan []string
	if err := pgPool.QueryRow(ctx,
		`SELECT tags FROM rimsky_nodes WHERE node_type = 'n1'`,
	).Scan(&tagsScan); err != nil {
		t.Fatalf("read tags: %v", err)
	}
	if len(tagsScan) != 0 {
		t.Errorf("tags default: got %v want empty []string", tagsScan)
	}

	var idxName string
	if err := pgPool.QueryRow(ctx, `
		SELECT indexname
		  FROM pg_indexes
		 WHERE schemaname = current_schema()
		   AND tablename = 'rimsky_nodes'
		   AND indexname = 'rimsky_nodes_tags_idx'
	`).Scan(&idxName); err != nil {
		t.Fatalf("query gin index: %v", err)
	}
	if idxName != "rimsky_nodes_tags_idx" {
		t.Errorf("gin index name: got %q want rimsky_nodes_tags_idx", idxName)
	}
}
