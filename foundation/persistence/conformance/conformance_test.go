// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package conformance

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fallguy/rimsky/foundation/internal/pgtest"
	"github.com/fallguy/rimsky/foundation/persistence"
	sqlitepersist "github.com/fallguy/rimsky/foundation/persistence/sqlite"
	"github.com/fallguy/rimsky/foundation/shared"

	// Driver registration for postgres. Pulled in so the suite test
	// file can drive both drivers from one place; pgtest itself
	// already imports the postgres driver but the blank import here
	// keeps the conformance_test.go file's intent explicit.
	_ "github.com/fallguy/rimsky/foundation/persistence/postgres"
)

func TestConformancePostgres(t *testing.T) {
	Suite(t, func(t *testing.T) persistence.Database {
		return pgtest.OpenDriver(context.Background(), t)
	}, postgresRawExec)
}

func TestConformanceSQLite(t *testing.T) {
	Suite(t, func(t *testing.T) persistence.Database {
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
	}, sqliteRawExec)
}

// postgresRawExec runs raw SQL against the postgres driver's pool via
// the pgtest-provided ExecForTest escape hatch (which keeps this test
// file outside the pgx-isolation depguard rule). Translates `?`
// placeholders to `$N` so the same SQL works against both drivers in
// the conformance suite.
func postgresRawExec(t *testing.T, d persistence.Database, sql string, args ...any) {
	t.Helper()
	pgSQL := translatePlaceholders(sql)
	pgtest.ExecForTest(context.Background(), t, d, pgSQL, args...)
}

// sqliteRawExec runs raw SQL against the sqlite driver's *sql.DB. The
// `?` placeholders pass through verbatim.
func sqliteRawExec(t *testing.T, d persistence.Database, sql string, args ...any) {
	t.Helper()
	db := sqlitepersist.DBFromDatabase(d)
	if _, err := db.ExecContext(context.Background(), sql, args...); err != nil {
		t.Fatalf("sqliteRawExec: %v\nsql: %s", err, sql)
	}
}

// translatePlaceholders rewrites `?` placeholders into `$1, $2, ...`
// for postgres. Naïve scan — does not honor `?` inside string
// literals, which is fine for the conformance test SQL (no `?`
// outside placeholders).
func translatePlaceholders(sql string) string {
	var b strings.Builder
	n := 0
	for _, r := range sql {
		if r == '?' {
			n++
			b.WriteString(fmt.Sprintf("$%d", n))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
