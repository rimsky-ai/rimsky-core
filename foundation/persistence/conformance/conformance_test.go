// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package conformance

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/fallguy/rimsky/foundation/internal/pgtest"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/shared"

	// Driver registrations.
	_ "github.com/fallguy/rimsky/foundation/persistence/postgres"
	_ "github.com/fallguy/rimsky/foundation/persistence/sqlite"
)

func TestConformancePostgres(t *testing.T) {
	Suite(t, func(t *testing.T) persistence.Driver {
		return pgtest.OpenDriver(context.Background(), t)
	})
}

func TestConformanceSQLite(t *testing.T) {
	Suite(t, func(t *testing.T) persistence.Driver {
		dir := t.TempDir()
		cfg := persistence.Config{
			Driver: "sqlite",
			SQLite: &persistence.SQLiteConfig{Path: filepath.Join(dir, "state.db")},
		}
		d, err := persistence.Open(context.Background(), cfg)
		if err != nil {
			t.Fatalf("open sqlite: %v", err)
		}
		t.Cleanup(func() { _ = d.Close() })
		if err := d.Migrate(context.Background(), shared.SilentLogger{}); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		return d
	})
}
