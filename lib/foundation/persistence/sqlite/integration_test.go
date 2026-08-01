// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package sqlite_test

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	sqlitedrv "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
)

func TestSQLiteForeignKeysEnabled(t *testing.T) {
	t.Parallel()
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

	db, ok := sqlitedrv.DBFromDatabaseForTest(d)
	if !ok {
		t.Fatal("DBFromDatabaseForTest: not a sqlite database")
	}
	var fk int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("pragma: %v", err)
	}
	if fk != 1 {
		t.Fatalf("PRAGMA foreign_keys = %d, want 1", fk)
	}
}

func TestSQLiteWALMode(t *testing.T) {
	t.Parallel()
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

	db, ok := sqlitedrv.DBFromDatabaseForTest(d)
	if !ok {
		t.Fatal("DBFromDatabaseForTest: not a sqlite database")
	}
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("pragma: %v", err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Fatalf("PRAGMA journal_mode = %q, want wal", mode)
	}
}

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
