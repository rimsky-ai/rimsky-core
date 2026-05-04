package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/fallguy/rimsky/foundation/persistence"
	_ "github.com/fallguy/rimsky/foundation/persistence/sqlite"
	"github.com/fallguy/rimsky/modeling/shared"
)

// TestSQLiteMigrationApplies verifies that the embedded init.sql applies
// cleanly against a fresh SQLite database and that re-running Migrate
// is a no-op (idempotent).
func TestSQLiteMigrationApplies(t *testing.T) {
	dir := t.TempDir()
	d, err := persistence.Open(context.Background(), persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(dir, "state.db")},
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	if err := d.Migrate(context.Background(), shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Idempotent re-run.
	if err := d.Migrate(context.Background(), shared.SilentLogger{}); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}
}
