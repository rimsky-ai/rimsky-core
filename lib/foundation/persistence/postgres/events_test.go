// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// events_test.go — exercises the postgres impls of
// persistence.EventTable for the typed-Kind discipline introduced by
// spec:2026-06-08-design-corpus-bootstrap Pass 2. Two load-bearing
// behaviors are pinned here:
//
//   - Write/read round-trip through events.Kind: an emit-site value
//     constructed via the typed constructors lands in the TEXT
//     column and reads back as a Kind whose String() matches.
//   - Unmarshal-boundary defense: a deliberately-corrupted kind
//     value inserted via raw SQL is rejected by the read path with
//     an ErrUnknownKind error — never silently coerced to a synthetic
//     "unknown" kind (per decision:event-log-kind-enum).

package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/internal/pgtest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func TestPGEvents_TypedKindRoundTrip(t *testing.T) {
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	store := d.Tables()
	// One operational kind and one signal-class kind — the column
	// only sees the wire string, but the read path must reconstruct
	// the typed Kind for either family.
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

// TestPGEvents_AppendRefusesZeroKind pins the write-side defense:
// passing the zero events.Kind value is a caller bug; the persistence
// driver refuses the insert rather than persisting an empty string
// the read path can't disambiguate from a corrupt row.
func TestPGEvents_AppendRefusesZeroKind(t *testing.T) {
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	store := d.Tables()
	err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.Events().Append(ctx, persistence.EventAppendInput{}, tx)
	})
	if err == nil {
		t.Fatal("Append with zero Kind did not error")
	}
}

// TestPGEvents_UnmarshalRejectsCorruptKind exercises the
// defensive-read posture: a deliberately-corrupted kind value (one
// that does not match any canonical operational name and is not
// shaped like a signal type-path) inserted via raw SQL is surfaced
// as an error by the read path, never silently coerced.
func TestPGEvents_UnmarshalRejectsCorruptKind(t *testing.T) {
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)
	store := d.Tables()
	// First insert a legitimate row so the table is exercised and
	// any kind-validation surface around the write path is honored
	// by inserting a real kind first.
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.Events().Append(ctx,
			persistence.EventAppendInput{Kind: events.KindWorkStarted()}, tx)
	}); err != nil {
		t.Fatalf("Append legitimate: %v", err)
	}
	// Now corrupt the row: write a deliberately-broken kind string
	// directly via the raw SQL escape hatch the test infrastructure
	// exposes for these defensive-error tests. The kind has no
	// slash (so it doesn't look like a signal path) and doesn't
	// match any canonical operational name.
	pgtest.ExecForTest(ctx, t, d, `UPDATE rimsky_events SET kind = $1`, "totally_made_up_kind")
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
