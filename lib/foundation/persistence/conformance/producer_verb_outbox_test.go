// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package conformance

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/internal/pgtest"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func TestProducerVerbOutboxPostgres(t *testing.T) {
	d := pgtest.OpenDriver(context.Background(), t)
	TestProducerVerbOutbox(t, d)
}

func TestProducerVerbOutboxSQLite(t *testing.T) {
	cfg := persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(t.TempDir(), "state.db")},
	}
	d, err := persistence.Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := d.Migrate(context.Background(), shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	TestProducerVerbOutbox(t, d)
}
