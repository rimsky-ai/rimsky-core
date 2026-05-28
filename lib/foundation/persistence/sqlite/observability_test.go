// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// openSQLite opens an in-process SQLite driver, runs migrations, and
// registers cleanup. The observability extensions (Queue.ListLive /
// CountLive, EventListFilter.KindIn, InstanceTable.CountByActive,
// FrameTable.{ListForObservability, GetForObservability}) all run
// against this driver per persistence-conformance, but they're new
// surface area so we exercise them here directly.
func openSQLite(t *testing.T) persistence.Database {
	t.Helper()
	dir := t.TempDir()
	d, err := persistence.Open(context.Background(), persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(dir, "obs.db")},
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

func TestSQLite_EventListFilter_KindIn(t *testing.T) {
	d := openSQLite(t)
	ctx := context.Background()
	store := d.Tables()
	events := store.Events()
	for _, kind := range []string{"work_started", "error", "work_completed"} {
		k := kind
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return events.Append(ctx, persistence.EventAppendInput{Kind: k}, tx)
		}); err != nil {
			t.Fatalf("append %s: %v", k, err)
		}
	}
	var res persistence.EventListResult
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := events.List(ctx, persistence.EventListFilter{
			KindIn: []string{"work_started", "work_completed"},
		}, persistence.ListPagination{Limit: 10}, tx)
		res = r
		return err
	}); err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(res.Events) != 2 {
		t.Fatalf("kindin filter returned %d, want 2", len(res.Events))
	}
}

func TestSQLite_QueueListLive_Empty(t *testing.T) {
	d := openSQLite(t)
	ctx := context.Background()
	res, err := d.Queue().ListLive(ctx, persistence.DispatchListFilter{}, persistence.ListPagination{Limit: 10})
	if err != nil {
		t.Fatalf("listlive: %v", err)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("expected 0 rows, got %d", len(res.Rows))
	}
	n, err := d.Queue().CountLive(ctx, persistence.DispatchListFilter{})
	if err != nil {
		t.Fatalf("countlive: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 count, got %d", n)
	}
}

func TestSQLite_InstanceCountByActive_Empty(t *testing.T) {
	d := openSQLite(t)
	var active, terminated int
	if err := d.Tables().Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		a, ter, err := d.Tables().Instances().CountByActive(ctx, tx)
		active, terminated = a, ter
		return err
	}); err != nil {
		t.Fatalf("countbyactive: %v", err)
	}
	if active != 0 || terminated != 0 {
		t.Fatalf("active=%d terminated=%d, want 0/0", active, terminated)
	}
}

func TestSQLite_FrameListForObservability_Empty(t *testing.T) {
	d := openSQLite(t)
	var res persistence.PaginatedListResult[persistence.FrameRow]
	if err := d.Tables().Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		r, err := d.Tables().Frames().ListForObservability(ctx,
			persistence.FrameListFilter{}, persistence.ListPagination{Limit: 10}, tx)
		res = r
		return err
	}); err != nil {
		t.Fatalf("listforobservability: %v", err)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("expected 0 rows, got %d", len(res.Rows))
	}
}

func TestSQLite_FrameGetForObservability_NotFound(t *testing.T) {
	d := openSQLite(t)
	var row *persistence.FrameRow
	if err := d.Tables().Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		r, err := d.Tables().Frames().GetForObservability(ctx, uuid.New(), tx)
		row = r
		return err
	}); err != nil {
		t.Fatalf("getforobservability: %v", err)
	}
	if row != nil {
		t.Fatalf("expected nil, got %+v", row)
	}
}
