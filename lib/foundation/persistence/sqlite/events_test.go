// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// events_test.go — exercises the sqlite impls of
// persistence.EventTable for the typed-Kind discipline introduced by
// spec:2026-06-08-design-corpus-bootstrap Pass 2. Mirrors the
// postgres-side events_test.go in scope:
//
//   - Write/read round-trip through events.Kind.
//   - Defensive-read posture against a deliberately-corrupted row.
//
// In-process (no testcontainers needed) per the sqlite tests' usual
// posture.

package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	sqlitepersist "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func openSQLiteForEvents(t *testing.T) persistence.Database {
	t.Helper()
	dir := t.TempDir()
	d, err := persistence.Open(context.Background(), persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(dir, "events.db")},
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := d.Migrate(context.Background(), shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func TestSQLiteEvents_TypedKindRoundTrip(t *testing.T) {
	d := openSQLiteForEvents(t)
	ctx := context.Background()
	store := d.Tables()
	cases := []events.Kind{
		events.KindWorkStarted(),
		events.KindAuthAccessAttempted(),
		events.SignalKind("terminal/success"),
		events.SignalKind("attribute/budget_cents/changed"),
	}
	for _, k := range cases {
		k := k
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return store.Events().Append(ctx, persistence.EventAppendInput{Kind: k}, tx)
		}); err != nil {
			t.Fatalf("Append(%s): %v", k.String(), err)
		}
	}
	var res persistence.EventListResult
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := store.Events().List(ctx, persistence.EventListFilter{},
			persistence.ListPagination{Limit: len(cases) + 5}, tx)
		res = r
		return err
	}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res.Events) != len(cases) {
		t.Fatalf("event count = %d, want %d", len(res.Events), len(cases))
	}
	seen := map[string]bool{}
	for _, e := range res.Events {
		if e.Kind.IsZero() {
			t.Fatalf("read returned zero Kind for KindRaw=%q", e.KindRaw)
		}
		if e.Kind.String() != e.KindRaw {
			t.Fatalf("Kind.String()=%q != KindRaw=%q", e.Kind.String(), e.KindRaw)
		}
		seen[e.KindRaw] = true
	}
	for _, k := range cases {
		if !seen[k.String()] {
			t.Fatalf("did not see wire form %q in the read result", k.String())
		}
	}
}

// TestSQLiteEvents_AppendRefusesZeroKind pins the write-side defense:
// the persistence driver refuses an empty / zero Kind value at the
// boundary rather than persisting a row that observability
// consumers can't filter on.
func TestSQLiteEvents_AppendRefusesZeroKind(t *testing.T) {
	d := openSQLiteForEvents(t)
	ctx := context.Background()
	store := d.Tables()
	err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.Events().Append(ctx, persistence.EventAppendInput{}, tx)
	})
	if err == nil {
		t.Fatal("Append with zero Kind did not error")
	}
}

// TestSQLiteEvents_UnmarshalRejectsCorruptKind exercises the
// defensive-read posture against a deliberately-corrupted kind
// inserted via raw SQL. The read path surfaces an ErrUnknownKind
// rather than coercing the row to a synthetic Kind.
func TestSQLiteEvents_UnmarshalRejectsCorruptKind(t *testing.T) {
	d := openSQLiteForEvents(t)
	ctx := context.Background()
	store := d.Tables()
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.Events().Append(ctx,
			persistence.EventAppendInput{Kind: events.KindWorkStarted()}, tx)
	}); err != nil {
		t.Fatalf("Append legitimate: %v", err)
	}
	// Use the test-only DBFromDatabase escape hatch to reach the
	// underlying sql.DB handle directly. The kind has no slash and
	// is not a canonical operational name, so ParseKindString
	// returns ErrUnknownKind.
	db := sqlitepersist.DBFromDatabase(d)
	if _, err := db.ExecContext(ctx,
		`UPDATE rimsky_events SET kind = 'totally_made_up_kind'`); err != nil {
		t.Fatalf("raw SQL update: %v", err)
	}
	err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := store.Events().List(ctx,
			persistence.EventListFilter{}, persistence.ListPagination{Limit: 5}, tx)
		return err
	})
	if err == nil {
		t.Fatal("List with corrupt kind did not error")
	}
	if !errors.Is(err, events.ErrUnknownKind) {
		t.Fatalf("List error = %v, want ErrUnknownKind", err)
	}
}
