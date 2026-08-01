// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func TestSweepParkedNodes_NilClockDefaultsInsteadOfPanicking(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(t.TempDir(), "sweep-parked.db")},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	err = SweepParkedNodes(ctx, ParkedSweepArgs{
		Persist:      d.Tables(),
		Queue:        d.Queue(),
		Clock:        nil,
		Logger:       shared.SilentLogger{},
		SupervisorID: "sup-sweep-nil-clock",
	})
	if err != nil {
		t.Fatalf("SweepParkedNodes with nil Clock must default to a real clock, not error: %v", err)
	}
}
