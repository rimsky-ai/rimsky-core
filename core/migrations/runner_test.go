package migrations_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/fallguy/rimsky/core/migrations"
	"github.com/fallguy/rimsky/core/shared"
)

var expectedTables = []string{
	"rimsky_migrations",
	"rimsky_templates",
	"rimsky_instances",
	"rimsky_nodes",
	"rimsky_resources",
	"rimsky_resource_versions",
	"rimsky_supervisors",
	"rimsky_dispatch",
	"rimsky_events",
	"rimsky_schedules",
}

func TestRun_AppliesAllMigrationsAndIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	container, err := tcpostgres.Run(ctx,
		"postgres:14-alpine",
		tcpostgres.WithDatabase("rimsky_test"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err, "start postgres container")
	t.Cleanup(func() {
		_ = container.Terminate(context.Background())
	})

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	// First run: applies all migrations.
	require.NoError(t, migrations.Run(ctx, pool, shared.SilentLogger{}))

	// Assert all expected rimsky_* tables exist in the public schema.
	for _, table := range expectedTables {
		var exists bool
		err := pool.QueryRow(ctx,
			`SELECT EXISTS(
				SELECT 1 FROM information_schema.tables
				WHERE table_schema = 'public' AND table_name = $1
			)`, table).Scan(&exists)
		require.NoError(t, err, "check table %s", table)
		require.True(t, exists, "table %s should exist", table)
	}

	// Count rows in rimsky_migrations after first run.
	var firstCount int
	require.NoError(t, pool.QueryRow(ctx, "SELECT COUNT(*) FROM rimsky_migrations").Scan(&firstCount))
	require.Greater(t, firstCount, 0, "at least one migration should have been recorded")

	// Second run: idempotent — no new entries.
	require.NoError(t, migrations.Run(ctx, pool, shared.SilentLogger{}))

	var secondCount int
	require.NoError(t, pool.QueryRow(ctx, "SELECT COUNT(*) FROM rimsky_migrations").Scan(&secondCount))
	require.Equal(t, firstCount, secondCount, "re-running Run should not record additional migrations")
}
