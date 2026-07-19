// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package config

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func TestStartScheduler_WiresAuthSweepLoop(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(dir, "state.db")},
	})
	if err != nil {
		t.Fatalf("persistence.Open: %v", err)
	}
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	clock := shared.NewControllableClock(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))

	_, hash, err := auth.Mint()
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	keyID := shared.UUID(uuid.New())
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return d.Tables().APIKeys().Insert(ctx, persistence.APIKey{
			ID:          keyID,
			Name:        "grace-expired",
			KeyHash:     hash[:],
			Permissions: []byte(`[{"action":"*"}]`),
			CreatedAt:   clock.Now(),
			RevokeAt:    ptrTime(clock.Now().Add(-time.Minute)),
		}, tx)
	}); err != nil {
		t.Fatalf("seed key: %v", err)
	}

	row, ok, err := d.Tables().APIKeys().GetByID(ctx, keyID, nil)
	if err != nil || !ok {
		t.Fatalf("precondition read: ok=%v err=%v", ok, err)
	}
	if row.RevokedAt != nil {
		t.Fatalf("precondition: seeded key must not already be revoked")
	}

	handle, err := StartScheduler(SchedulerConfig{
		Driver:            d,
		Clock:             clock,
		Logger:            shared.SilentLogger{},
		AuthSweepInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("StartScheduler: %v", err)
	}
	t.Cleanup(func() { _ = handle.Shutdown(context.Background()) })

	for {
		row, ok, err := d.Tables().APIKeys().GetByID(ctx, keyID, nil)
		if err != nil {
			t.Fatalf("poll GetByID: %v", err)
		}
		if !ok {
			t.Fatalf("poll GetByID: key disappeared")
		}
		if row.RevokedAt != nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }
