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

	"github.com/rimsky-ai/rimsky-core/lib/foundation/internal/pgtest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	sqlitepersist "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"

	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/postgres"
)

func TestConformancePostgres(t *testing.T) {
	Suite(t, func(t *testing.T) persistence.Database {
		return pgtest.OpenDriver(context.Background(), t)
	}, postgresRawExec, postgresRawQuery)
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
	}, sqliteRawExec, sqliteRawQuery)
}

func postgresRawExec(t *testing.T, d persistence.Database, sql string, args ...any) {
	t.Helper()
	pgSQL := translatePlaceholders(sql)
	pgtest.ExecForTest(context.Background(), t, d, pgSQL, args...)
}

func postgresRawQuery(t *testing.T, d persistence.Database, sql string, args ...any) []RawQueryRow {
	t.Helper()
	pgSQL := translatePlaceholders(sql)
	return pgtest.QueryRowsForTest(context.Background(), t, d, pgSQL, args...)
}

func sqliteRawExec(t *testing.T, d persistence.Database, sql string, args ...any) {
	t.Helper()
	db, ok := sqlitepersist.DBFromDatabaseForTest(d)
	if !ok {
		t.Fatal("DBFromDatabaseForTest: not a sqlite database")
	}
	if _, err := db.ExecContext(context.Background(), sql, args...); err != nil {
		t.Fatalf("sqliteRawExec: %v\nsql: %s", err, sql)
	}
}

func sqliteRawQuery(t *testing.T, d persistence.Database, sql string, args ...any) []RawQueryRow {
	t.Helper()
	db, ok := sqlitepersist.DBFromDatabaseForTest(d)
	if !ok {
		t.Fatal("DBFromDatabaseForTest: not a sqlite database")
	}
	rows, err := db.QueryContext(context.Background(), sql, args...)
	if err != nil {
		t.Fatalf("sqliteRawQuery: %v\nsql: %s", err, sql)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("sqliteRawQuery columns: %v", err)
	}
	var out []RawQueryRow
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("sqliteRawQuery scan: %v", err)
		}
		row := make(RawQueryRow, len(cols))
		for i, c := range cols {
			row[c] = vals[i]
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("sqliteRawQuery rows.Err: %v", err)
	}
	return out
}

func translatePlaceholders(sql string) string {
	var b strings.Builder
	n := 0
	inString := false
	for _, r := range sql {
		if r == '\'' {
			inString = !inString
			b.WriteRune(r)
			continue
		}
		if r == '?' && !inString {
			n++
			b.WriteString(fmt.Sprintf("$%d", n))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
