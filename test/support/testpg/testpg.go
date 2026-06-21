// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package testpg

import (
	"context"
	"fmt"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/postgres"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/pgpool"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

var sharedPool = pgpool.New(pgpool.Config{
	Image:        "postgres:14-alpine",
	Database:     "rimsky",
	User:         "rimsky",
	Password:     "rimsky",
	InitTemplate: migrateTemplate,
})

func StartFreshPostgresDSN(ctx context.Context, t *testing.T) (string, func()) {
	t.Helper()
	return sharedPool.Acquire(ctx, t), func() {}
}

func migrateTemplate(ctx context.Context, dsn string) error {
	d, err := persistence.Open(ctx, persistence.Config{
		Driver:   "postgres",
		Postgres: &persistence.PostgresConfig{DSN: dsn},
	})
	if err != nil {
		return fmt.Errorf("open template driver: %w", err)
	}
	defer func() { _ = d.Close() }()
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		return fmt.Errorf("migrate template: %w", err)
	}
	return nil
}
