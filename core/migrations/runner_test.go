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

	"github.com/fallguy/rimsky/core/internal/pgtest"
	"github.com/fallguy/rimsky/core/migrations"
	"github.com/fallguy/rimsky/core/shared"
)

var expectedTables = []string{
	"rimsky_migrations",
	"rimsky_templates",
	"rimsky_instances",
	"rimsky_nodes",
	"rimsky_supervisors",
	"rimsky_dispatch",
	"rimsky_events",
	"rimsky_schedules",
	"rimsky_node_attributes",
	"rimsky_lock_holders",
	"rimsky_claim_holders",
	"rimsky_frames",
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

// TestMigration002FrameResolutionSchema verifies that 002-frame-resolution.sql,
// when applied through the migration runner, produces the schema shape required
// by the frame-resolution design (rimsky_frames table, frame_id columns on
// dispatch/nodes/lock_holders/claim_holders, kill_requested removed, supporting
// indexes present).
func TestMigration002FrameResolutionSchema(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	// rimsky_frames table must exist.
	var framesTableExists bool
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT EXISTS(
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = 'rimsky_frames'
		)`).Scan(&framesTableExists))
	require.True(t, framesTableExists, "rimsky_frames table should exist")

	// All four frame_id columns must exist.
	frameIDColumns := []struct {
		table  string
		column string
	}{
		{"rimsky_dispatch", "frame_id"},
		{"rimsky_nodes", "frame_id"},
		{"rimsky_lock_holders", "frame_id"},
		{"rimsky_claim_holders", "frame_id"},
	}
	for _, c := range frameIDColumns {
		var exists bool
		err := pool.QueryRow(ctx,
			`SELECT EXISTS(
				SELECT 1 FROM information_schema.columns
				WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2
			)`, c.table, c.column).Scan(&exists)
		require.NoError(t, err, "check %s.%s", c.table, c.column)
		require.True(t, exists, "column %s.%s should exist", c.table, c.column)
	}

	// rimsky_nodes.kill_requested must NOT exist (removed from 001-initial.sql per pre-v1 break-freely; 002 does not drop it).
	var killRequestedExists bool
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT EXISTS(
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = 'rimsky_nodes' AND column_name = 'kill_requested'
		)`).Scan(&killRequestedExists))
	require.False(t, killRequestedExists, "rimsky_nodes.kill_requested should not exist (never declared in 001-initial.sql)")

	// All six supporting indexes must be present.
	expectedIndexes := []string{
		"uq_rimsky_frames_running",
		"uq_rimsky_frames_coalesce_queued",
		"idx_rimsky_frames_queued",
		"idx_rimsky_dispatch_frame",
		"idx_rimsky_dispatch_frame_claimed",
		"idx_rimsky_nodes_frame_state",
	}
	for _, idx := range expectedIndexes {
		var exists bool
		err := pool.QueryRow(ctx,
			`SELECT EXISTS(
				SELECT 1 FROM pg_indexes
				WHERE schemaname = 'public' AND indexname = $1
			)`, idx).Scan(&exists)
		require.NoError(t, err, "check index %s", idx)
		require.True(t, exists, "index %s should exist", idx)
	}
}
