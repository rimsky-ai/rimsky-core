// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime_test

import (
	"context"
	"crypto/sha256"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

func TestSweepRotationGrace(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	d, err := persistence.Open(ctx, persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(dir, "state.db")},
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	tables := d.Tables()

	clock := shared.NewControllableClock(time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC))

	keyID := uuid.New()
	hash := sha256.Sum256([]byte("rk_sweep_target"))
	past := clock.Now().Add(-1 * time.Minute)
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := tables.APIKeys().Insert(ctx, persistence.APIKey{
			ID:          keyID,
			KeyHash:     hash[:],
			Name:        "to-sweep",
			Permissions: []byte(`[{"action":"*"}]`),
			CreatedAt:   clock.Now().Add(-1 * time.Hour),
		}, tx); err != nil {
			return err
		}
		return tables.APIKeys().SetRevokeAt(ctx, keyID, past, tx)
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	logger := shared.NewCapturingLogger()
	n, err := runtime.SweepRotationGrace(ctx, tables, clock, logger)
	if err != nil {
		t.Fatalf("SweepRotationGrace: %v", err)
	}
	if n != 1 {
		t.Fatalf("sweep returned %d; want 1", n)
	}

	row, ok, err := tables.APIKeys().GetByID(ctx, keyID, nil)
	if err != nil || !ok || row.RevokedAt == nil {
		t.Fatalf("post-sweep row: err=%v ok=%v revoked_at=%v", err, ok, row.RevokedAt)
	}

	n, err = runtime.SweepRotationGrace(ctx, tables, clock, logger)
	if err != nil {
		t.Fatalf("re-sweep: %v", err)
	}
	if n != 0 {
		t.Fatalf("re-sweep returned %d; want 0", n)
	}

	var auditFound int
	if err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		rl, err := tables.Events().List(ctx, persistence.EventListFilter{Kind: auth.EventKeyRevoked}, persistence.ListPagination{}, tx)
		if err != nil {
			return err
		}
		for _, e := range rl.Events {
			if e.KindRaw == auth.EventKeyRevoked {
				auditFound++
				if reason, _ := e.Payload["reason"].(string); reason != string(auth.RevokeReasonRotationGrace) {
					t.Errorf("audit reason: got %q want %q", reason, auth.RevokeReasonRotationGrace)
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("Events.List: %v", err)
	}
	if auditFound != 1 {
		t.Fatalf("expected 1 auth.key_revoked event; got %d", auditFound)
	}
}
