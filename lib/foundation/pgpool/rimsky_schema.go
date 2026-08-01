// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package pgpool

import (
	"context"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/postgres"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

const RimskySchemaImage = "postgres:15-alpine"

func NewRimskySchemaPool() *Pool {
	return New(Config{
		Image:        RimskySchemaImage,
		Database:     "rimsky",
		User:         "rimsky",
		Password:     "rimsky",
		InitTemplate: migrateRimskySchema,
	})
}

func migrateRimskySchema(ctx context.Context, dsn string) error {
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
