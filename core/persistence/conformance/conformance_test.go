package conformance

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/fallguy/rimsky/core/internal/pgtest"
	"github.com/fallguy/rimsky/core/persistence"
	"github.com/fallguy/rimsky/core/shared"

	// Driver registrations.
	_ "github.com/fallguy/rimsky/core/persistence/postgres"
	_ "github.com/fallguy/rimsky/core/persistence/sqlite"
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
