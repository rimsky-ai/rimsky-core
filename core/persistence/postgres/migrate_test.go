package postgres_test

import (
	"context"
	"testing"

	"github.com/fallguy/rimsky/core/internal/pgtest"
	"github.com/fallguy/rimsky/core/persistence"
	_ "github.com/fallguy/rimsky/core/persistence/postgres"
	"github.com/fallguy/rimsky/core/shared"
)

// TestMigrateAgainstTestcontainers exercises persistence.Migrate end-to-
// end against a fresh testcontainers Postgres. Validates that the
// embedded SQL applies cleanly and is idempotent on re-run.
func TestMigrateAgainstTestcontainers(t *testing.T) {
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
	// Idempotency: second Migrate is a no-op.
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}
}
