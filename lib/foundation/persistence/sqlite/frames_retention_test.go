// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

func TestPruneTraceForRetention_NoTxPanic(t *testing.T) {
	t.Parallel()
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
