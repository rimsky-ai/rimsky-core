// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// frames_retention_test.go — regression guard for the FrameTable
// retention sweep (E10). PruneOldRunsForRetention is a standalone sweep
// with no caller-supplied tx; it must run directly against the db handle.
// A prior bug had it call s.q(nil), which trips the no-nil-tx contract and
// panics the scheduler tick the moment retention is enabled.
//
// @concept: frame

package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// TestPruneOldRunsForRetention_NoTxPanic asserts the run-tree retention
// sweep does not panic with a nil-tx error when invoked the way the
// scheduler tick invokes it (no surrounding Tables.Transaction). Run
// against an empty DB — the method must execute its DELETE and return 0,
// not panic on s.q(nil).
func TestPruneOldRunsForRetention_NoTxPanic(t *testing.T) {
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

	n, err := d.Tables().Frames().PruneOldRunsForRetention(ctx, 2)
	if err != nil {
		t.Fatalf("PruneOldRunsForRetention: %v", err)
	}
	if n != 0 {
		t.Fatalf("PruneOldRunsForRetention on empty DB deleted %d rows, want 0", n)
	}
}
