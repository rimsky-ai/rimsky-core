// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package sqlite_test

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/rimsky-ai/rimsky-core/foundation/persistence"
	pgsqlite "github.com/rimsky-ai/rimsky-core/foundation/persistence/sqlite"
)

// TestSQLiteForeignKeysEnabled confirms the FK-enforcement PRAGMA is
// active on driver-issued connections. Without it, the rimsky_claim_handles
// → rimsky_claim_holders ON DELETE CASCADE wouldn't fire, which would
// silently break auto-terminal cleanup.
//
// Queried against the *driver's* underlying sql.DB (via DBFromDatabase) so
// the test can't pass against a parallel handle whose PRAGMA state happens
// to be set independently — the contract under test is that the driver's
// own connections boot with FKs on.
func TestSQLiteForeignKeysEnabled(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "fk.db")
	d, err := persistence.Open(context.Background(), persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: dbPath},
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	db := pgsqlite.DBFromDatabase(d)
	var fk int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("pragma: %v", err)
	}
	if fk != 1 {
		t.Fatalf("PRAGMA foreign_keys = %d, want 1", fk)
	}
}

// TestSQLiteWALMode confirms _journal_mode=WAL takes effect on driver
// connections. Queried against the *driver's* underlying sql.DB (via
// DBFromDatabase) so the test can't pass against a parallel handle.
func TestSQLiteWALMode(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "wal.db")
	d, err := persistence.Open(context.Background(), persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: dbPath},
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	db := pgsqlite.DBFromDatabase(d)
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("pragma: %v", err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Fatalf("PRAGMA journal_mode = %q, want wal", mode)
	}
}

// TestSQLiteStartupBanner verifies the dev-only-driver warning is logged
// at Open time. Operators must see this loudly per spec §1.
func TestSQLiteStartupBanner(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	defer slog.SetDefault(prev)
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))

	dir := t.TempDir()
	d, err := persistence.Open(context.Background(), persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(dir, "banner.db")},
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = d.Close() }()

	if !strings.Contains(buf.String(), "SQLite driver is for local development only") {
		t.Fatalf("startup banner not logged; got:\n%s", buf.String())
	}
}
