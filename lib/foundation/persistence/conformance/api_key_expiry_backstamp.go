// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: api-key

package conformance

import (
	"context"
	"io/fs"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

const expiryEventMigration = "050-api-key-expiry-event.sql"

func testUpgradeReportsNoExpiryThatPassedBeforeIt(
	t *testing.T, d persistence.Database,
	migrations fs.FS,
	rawExec func(t *testing.T, d persistence.Database, sql string, args ...any),
) {
	t.Helper()
	ctx := context.Background()
	keys := d.Tables().APIKeys()

	now := time.Now().UTC()
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	alreadyExpired := uuid.New()
	alreadyExpiredHash := sha256Of([]byte("rk_already_expired"))
	stillLive := uuid.New()
	stillLiveHash := sha256Of([]byte("rk_still_live"))
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := keys.Insert(ctx, persistence.APIKey{
			ID:          alreadyExpired,
			KeyHash:     alreadyExpiredHash[:],
			Name:        "already-expired",
			Permissions: []byte(`[{"action":"*"}]`),
			CreatedAt:   past.Add(-time.Hour),
			ExpiresAt:   &past,
		}, tx); err != nil {
			return err
		}
		return keys.Insert(ctx, persistence.APIKey{
			ID:          stillLive,
			KeyHash:     stillLiveHash[:],
			Name:        "still-live",
			Permissions: []byte(`[{"action":"*"}]`),
			CreatedAt:   past.Add(-time.Hour),
			ExpiresAt:   &future,
		}, tx)
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	rawExec(t, d, "ALTER TABLE rimsky_api_keys DROP COLUMN expiry_event_at")
	sql, err := fs.ReadFile(migrations, expiryEventMigration)
	if err != nil {
		t.Fatalf("read %s: %v", expiryEventMigration, err)
	}
	rawExec(t, d, string(sql))

	var swept []persistence.APIKey
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		swept, err = keys.SweepExpired(ctx, now, tx)
		return err
	}); err != nil {
		t.Fatalf("SweepExpired at the upgrade: %v", err)
	}
	for _, k := range swept {
		if k.ID == alreadyExpired {
			t.Fatalf("the first sweep after the upgrade reported an expiry that passed before it: %s", k.Name)
		}
	}

	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		swept, err = keys.SweepExpired(ctx, future.Add(time.Minute), tx)
		return err
	}); err != nil {
		t.Fatalf("SweepExpired after the live key's end: %v", err)
	}
	found := false
	for _, k := range swept {
		if k.ID == alreadyExpired {
			t.Fatalf("a back-stamped key must never expire again: %s", k.Name)
		}
		if k.ID == stillLive {
			found = true
		}
	}
	if !found {
		t.Fatalf("the sweep must report an expiry that passes after the upgrade; it reported none for %s", "still-live")
	}
}
