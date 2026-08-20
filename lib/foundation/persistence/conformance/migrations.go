// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package conformance

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func testMigrationIdempotency(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}

	var (
		wg   sync.WaitGroup
		errs [2]error
	)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = d.Migrate(ctx, shared.SilentLogger{})
		}(i)
	}
	wg.Wait()
	for i, e := range errs {
		if e != nil {
			t.Fatalf("concurrent Migrate %d: %v", i, e)
		}
	}

	if d.Queue() == nil {
		t.Fatalf("Queue() nil after re-migrate")
	}
	if d.Tables() == nil {
		t.Fatalf("Tables() nil after re-migrate")
	}
}

// @decision: migrations-append-only-numbered
func testMigrationRejectsAChangedFile(
	t *testing.T, d persistence.Database,
	rawExec func(t *testing.T, d persistence.Database, sql string, args ...any),
	rawQuery func(t *testing.T, d persistence.Database, sql string, args ...any) []RawQueryRow,
) {
	ctx := context.Background()
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("baseline migrate: %v", err)
	}

	rows := rawQuery(t, d, "SELECT filename FROM rimsky_migrations WHERE digest IS NULL")
	if len(rows) != 0 {
		t.Fatalf("every applied migration must record a digest; %d rows have none", len(rows))
	}
	rows = rawQuery(t, d, "SELECT filename FROM rimsky_migrations ORDER BY filename")
	if len(rows) == 0 {
		t.Fatalf("no applied migrations recorded")
	}
	victim := fmt.Sprint(rows[0]["filename"])

	rawExec(t, d, "UPDATE rimsky_migrations SET digest = ? WHERE filename = ?", "a-different-file", victim)

	err := d.Migrate(ctx, shared.SilentLogger{})
	if err == nil {
		t.Fatalf("migrate must refuse a file whose contents no longer match what was applied")
	}
	if !strings.Contains(err.Error(), victim) {
		t.Fatalf("the refusal must name the changed file %s; got %v", victim, err)
	}

	rawExec(t, d, "UPDATE rimsky_migrations SET digest = NULL WHERE filename = ?", victim)
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("a row with no recorded digest must be backfilled, not refused: %v", err)
	}
	rows = rawQuery(t, d, "SELECT digest FROM rimsky_migrations WHERE filename = ?", victim)
	if len(rows) != 1 || rows[0]["digest"] == nil {
		t.Fatalf("the backfill must record a digest for %s; got %v", victim, rows)
	}
}
