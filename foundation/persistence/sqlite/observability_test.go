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

	"github.com/fallguy/rimsky/foundation/persistence"
	_ "github.com/fallguy/rimsky/foundation/persistence/sqlite"
	"github.com/fallguy/rimsky/modeling/shared"
)

// openSQLite opens an in-process SQLite driver, runs migrations, and
// registers cleanup. The observability extensions (Queue.ListLive /
// CountLive, EventListFilter.KindIn, InstanceStore.CountByActive,
// FrameStore.{ListForObservability, GetForObservability}) all run
// against this driver per persistence-conformance, but they're new
// surface area so we exercise them here directly.
func openSQLite(t *testing.T) persistence.Driver {
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
	events := d.Store().Events()
	for _, kind := range []string{"work_started", "error", "work_completed"} {
		if err := events.Append(ctx, persistence.EventAppendInput{Kind: kind}, nil); err != nil {
			t.Fatalf("append %s: %v", kind, err)
		}
	}
	res, err := events.List(ctx, persistence.EventListFilter{
		KindIn: []string{"work_started", "work_completed"},
	}, persistence.ListPagination{Limit: 10}, nil)
	if err != nil {
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
	active, terminated, err := d.Store().Instances().CountByActive(context.Background(), nil)
	if err != nil {
		t.Fatalf("countbyactive: %v", err)
	}
	if active != 0 || terminated != 0 {
		t.Fatalf("active=%d terminated=%d, want 0/0", active, terminated)
	}
}

func TestSQLite_FrameListForObservability_Empty(t *testing.T) {
	d := openSQLite(t)
	res, err := d.Store().Frames().ListForObservability(context.Background(),
		persistence.FrameListFilter{}, persistence.ListPagination{Limit: 10}, nil)
	if err != nil {
		t.Fatalf("listforobservability: %v", err)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("expected 0 rows, got %d", len(res.Rows))
	}
}

func TestSQLite_FrameGetForObservability_NotFound(t *testing.T) {
	d := openSQLite(t)
	row, err := d.Store().Frames().GetForObservability(context.Background(), uuid.New(), nil)
	if err != nil {
		t.Fatalf("getforobservability: %v", err)
	}
	if row != nil {
		t.Fatalf("expected nil, got %+v", row)
	}
}
