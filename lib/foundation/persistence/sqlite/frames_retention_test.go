// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: frame

package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// TestPruneTraceForRetention_NoTxPanic asserts the trace retention sweep
// does not panic with a nil-tx error when invoked the way the scheduler
// tick invokes it (no surrounding Tables.Transaction). Run against an
// empty DB — the method must execute its DELETE and return 0, not panic
// on s.q(nil). The zero cutoff exercises the count-only bound (no time
// dimension).
func TestPruneTraceForRetention_NoTxPanic(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(dir, "retention.db")},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	n, err := d.Tables().Frames().PruneTraceForRetention(ctx, 2, time.Time{})
	if err != nil {
		t.Fatalf("PruneTraceForRetention: %v", err)
	}
	if n != 0 {
		t.Fatalf("PruneTraceForRetention on empty DB deleted %d rows, want 0", n)
	}
}
