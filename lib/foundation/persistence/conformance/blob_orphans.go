// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package conformance

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func mustInsertOrphan(t *testing.T, ctx context.Context, store persistence.Tables, orphans persistence.BlobOrphanTable, row persistence.BlobOrphanRow) {
	t.Helper()
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return orphans.Insert(ctx, row, tx)
	}); err != nil {
		t.Fatalf("Insert(%s): %v", row.Handle, err)
	}
}

func TestBlobOrphans(t *testing.T, d persistence.Database) {
	t.Helper()
	ctx := context.Background()
	store := d.Tables()
	orphans := store.BlobOrphans()

	base := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)

	t.Run("InsertIdempotentOnConflict", func(t *testing.T) {
		row := persistence.BlobOrphanRow{
			Handle: "orphan-dedupe-1", Backend: "mem",
			OrphanedAt: base, ReapAfter: base.Add(time.Hour),
		}
		mustInsertOrphan(t, ctx, store, orphans, row)
		row2 := row
		row2.ReapAfter = base.Add(2 * time.Hour)
		mustInsertOrphan(t, ctx, store, orphans, row2)
		got, err := orphans.DueBefore(ctx, base.Add(90*time.Minute), "mem", 1000)
		if err != nil {
			t.Fatalf("DueBefore: %v", err)
		}
		count := 0
		for _, r := range got {
			if r.Handle == row.Handle {
				count++
				if !r.ReapAfter.Equal(row.ReapAfter) {
					t.Fatalf("ON CONFLICT DO NOTHING must keep the first reap_after: got %v want %v", r.ReapAfter, row.ReapAfter)
				}
			}
		}
		if count != 1 {
			t.Fatalf("expected exactly one row for handle %q, got %d", row.Handle, count)
		}
	})

	t.Run("DueBeforeCutoffAndOrdering", func(t *testing.T) {
		early := persistence.BlobOrphanRow{Handle: "orphan-order-early", Backend: "mem", OrphanedAt: base, ReapAfter: base.Add(1 * time.Hour)}
		mid := persistence.BlobOrphanRow{Handle: "orphan-order-mid", Backend: "mem", OrphanedAt: base, ReapAfter: base.Add(2 * time.Hour)}
		late := persistence.BlobOrphanRow{Handle: "orphan-order-late", Backend: "mem", OrphanedAt: base, ReapAfter: base.Add(3 * time.Hour)}
		for _, r := range []persistence.BlobOrphanRow{late, early, mid} {
			mustInsertOrphan(t, ctx, store, orphans, r)
		}
		got, err := orphans.DueBefore(ctx, base.Add(150*time.Minute), "mem", 1000)
		if err != nil {
			t.Fatalf("DueBefore: %v", err)
		}
		var order []string
		for _, r := range got {
			switch r.Handle {
			case early.Handle, mid.Handle, late.Handle:
				order = append(order, r.Handle)
			}
		}
		if len(order) != 2 || order[0] != early.Handle || order[1] != mid.Handle {
			t.Fatalf("DueBefore cutoff/order: got %v, want [%s %s] (late excluded, ascending reap_after)", order, early.Handle, mid.Handle)
		}
	})

	t.Run("DueBeforeLimit", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			r := persistence.BlobOrphanRow{
				Handle:     fmt.Sprintf("orphan-limit-%d", i),
				Backend:    "mem",
				OrphanedAt: base,
				ReapAfter:  base.Add(time.Duration(i) * time.Minute),
			}
			mustInsertOrphan(t, ctx, store, orphans, r)
		}
		got, err := orphans.DueBefore(ctx, base.Add(time.Hour), "mem", 2)
		if err != nil {
			t.Fatalf("DueBefore: %v", err)
		}
		if len(got) > 2 {
			t.Fatalf("DueBefore limit=2 returned %d rows, want <=2", len(got))
		}
		sawLimitPrefixed := false
		for _, r := range got {
			if strings.HasPrefix(r.Handle, "orphan-limit-") {
				sawLimitPrefixed = true
			}
		}
		if !sawLimitPrefixed {
			t.Fatalf("DueBefore limit=2: expected at least one of the newly inserted rows to surface, got %+v", got)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		row := persistence.BlobOrphanRow{Handle: "orphan-delete-me", Backend: "mem", OrphanedAt: base, ReapAfter: base}
		mustInsertOrphan(t, ctx, store, orphans, row)
		if err := orphans.Delete(ctx, row.Handle); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if err := orphans.Delete(ctx, row.Handle); err != nil {
			t.Fatalf("Delete (idempotent): %v", err)
		}
		got, err := orphans.DueBefore(ctx, base.Add(time.Hour), "mem", 1000)
		if err != nil {
			t.Fatalf("DueBefore: %v", err)
		}
		for _, r := range got {
			if r.Handle == row.Handle {
				t.Fatalf("deleted handle %q still present", row.Handle)
			}
		}
	})

	t.Run("DueBeforeFiltersByBackendAtTheQueryLevel", func(t *testing.T) {
		mustInsertOrphan(t, ctx, store, orphans, persistence.BlobOrphanRow{
			Handle: "orphan-backend-mem", Backend: "mem", OrphanedAt: base, ReapAfter: base,
		})
		mustInsertOrphan(t, ctx, store, orphans, persistence.BlobOrphanRow{
			Handle: "orphan-backend-fs", Backend: "filesystem", OrphanedAt: base, ReapAfter: base,
		})
		got, err := orphans.DueBefore(ctx, base.Add(time.Hour), "mem", 1000)
		if err != nil {
			t.Fatalf("DueBefore: %v", err)
		}
		for _, r := range got {
			if r.Backend != "mem" {
				t.Fatalf("DueBefore(backend=mem) returned a row from backend %q; the filter must apply at the query level, "+
					"not just by the caller discarding rows after the fact", r.Backend)
			}
			if r.Handle == "orphan-backend-fs" {
				t.Fatalf("DueBefore(backend=mem) must not return the filesystem-backend row")
			}
		}
		gotFS, err := orphans.DueBefore(ctx, base.Add(time.Hour), "filesystem", 1000)
		if err != nil {
			t.Fatalf("DueBefore: %v", err)
		}
		sawFS := false
		for _, r := range gotFS {
			if r.Handle == "orphan-backend-fs" {
				sawFS = true
			}
			if r.Backend != "filesystem" {
				t.Fatalf("DueBefore(backend=filesystem) returned a row from backend %q", r.Backend)
			}
		}
		if !sawFS {
			t.Fatalf("DueBefore(backend=filesystem) must return the filesystem-backend row")
		}
	})
}
