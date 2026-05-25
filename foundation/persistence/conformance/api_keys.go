// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Conformance suite for persistence.APIKeyTable. Driver-agnostic;
// invoked by the per-driver test wrappers.
//
// @concept: api-key

package conformance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	"github.com/fallguyconsulting/rimsky/foundation/shared"
)

// TestAPIKeys exercises the APIKeyTable surface against the supplied
// driver. Called from postgres + sqlite per-driver wrappers.
func TestAPIKeys(t *testing.T, d persistence.Database) {
	t.Helper()
	ctx := context.Background()
	tables := d.Tables()
	keys := tables.APIKeys()

	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)

	// Insert + GetByID round-trip.
	t.Run("InsertGetByID", func(t *testing.T) {
		id := uuid.New()
		hash := sha256Of([]byte("rk_test_one"))
		err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return keys.Insert(ctx, persistence.APIKey{
				ID:          id,
				KeyHash:     hash[:],
				Name:        "test-one",
				Permissions: []byte(`[{"action":"*"}]`),
				CreatedAt:   now,
			}, tx)
		})
		if err != nil {
			t.Fatalf("Insert: %v", err)
		}
		got, ok, err := keys.GetByID(ctx, id, nil)
		if err != nil || !ok {
			t.Fatalf("GetByID: err=%v ok=%v", err, ok)
		}
		if got.Name != "test-one" || !bytes.Equal(got.KeyHash, hash[:]) {
			t.Fatalf("round-trip mismatch: %+v", got)
		}
	})

	// GetByName returns only active rows.
	t.Run("GetByName_Active", func(t *testing.T) {
		id := uuid.New()
		hash := sha256Of([]byte("rk_test_active"))
		mustInsert(t, ctx, tables, keys, persistence.APIKey{
			ID: id, KeyHash: hash[:], Name: "active-only",
			Permissions: []byte(`[{"action":"*"}]`), CreatedAt: now,
		})
		got, ok, err := keys.GetByName(ctx, "active-only", nil)
		if err != nil || !ok || got.ID != id {
			t.Fatalf("GetByName active: err=%v ok=%v got.ID=%v", err, ok, got.ID)
		}
		// Mark revoked → GetByName returns false.
		mustMarkRevoked(t, ctx, tables, keys, id, now)
		_, ok, err = keys.GetByName(ctx, "active-only", nil)
		if err != nil {
			t.Fatalf("GetByName after revoke: %v", err)
		}
		if ok {
			t.Fatalf("GetByName must return ok=false for revoked row")
		}
	})

	// GetByHash returns rows regardless of active status.
	t.Run("GetByHash_AllStatuses", func(t *testing.T) {
		id := uuid.New()
		hash := sha256Of([]byte("rk_test_byhash"))
		mustInsert(t, ctx, tables, keys, persistence.APIKey{
			ID: id, KeyHash: hash[:], Name: "by-hash",
			Permissions: []byte(`[{"action":"*"}]`), CreatedAt: now,
		})
		// Active row.
		got, ok, err := keys.GetByHash(ctx, hash[:], nil)
		if err != nil || !ok || got.ID != id {
			t.Fatalf("GetByHash active: err=%v ok=%v", err, ok)
		}
		// Revoke and re-fetch — GetByHash still finds it.
		mustMarkRevoked(t, ctx, tables, keys, id, now)
		got, ok, err = keys.GetByHash(ctx, hash[:], nil)
		if err != nil || !ok || got.RevokedAt == nil {
			t.Fatalf("GetByHash revoked: err=%v ok=%v revoked_at=%v", err, ok, got.RevokedAt)
		}
	})

	// List filters revoked, glob name.
	t.Run("List_GlobAndRevoked", func(t *testing.T) {
		var liveIDs []shared.UUID
		for i, name := range []string{"alpha", "alphabet", "beta"} {
			id := uuid.New()
			hash := sha256Of([]byte("rk_list_" + name))
			mustInsert(t, ctx, tables, keys, persistence.APIKey{
				ID: id, KeyHash: hash[:], Name: name,
				Permissions: []byte(`[{"action":"*"}]`),
				CreatedAt:   now.Add(time.Duration(i) * time.Minute),
			})
			liveIDs = append(liveIDs, id)
		}
		mustMarkRevoked(t, ctx, tables, keys, liveIDs[1], now)
		// Excluding revoked.
		got, err := keys.List(ctx, false, "alpha*", nil)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 1 || got[0].Name != "alpha" {
			t.Fatalf("List excl-revoked: got %d rows (%+v)", len(got), names(got))
		}
		// Including revoked.
		got, err = keys.List(ctx, true, "alpha*", nil)
		if err != nil {
			t.Fatalf("List incl-revoked: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("List incl-revoked: got %d rows (%+v)", len(got), names(got))
		}
	})

	// ActiveCount predicate.
	t.Run("ActiveCount", func(t *testing.T) {
		// Reset by revoking everything in the table.
		all, err := keys.List(ctx, true, "", nil)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, k := range all {
			if k.RevokedAt == nil {
				mustMarkRevoked(t, ctx, tables, keys, k.ID, now)
			}
		}
		n, err := keys.ActiveCount(ctx, now, nil)
		if err != nil || n != 0 {
			t.Fatalf("ActiveCount empty: n=%d err=%v", n, err)
		}
		// One active.
		id := uuid.New()
		hash := sha256Of([]byte("rk_active_count"))
		mustInsert(t, ctx, tables, keys, persistence.APIKey{
			ID: id, KeyHash: hash[:], Name: "count-active",
			Permissions: []byte(`[{"action":"*"}]`), CreatedAt: now,
		})
		n, err = keys.ActiveCount(ctx, now, nil)
		if err != nil || n != 1 {
			t.Fatalf("ActiveCount after insert: n=%d err=%v", n, err)
		}
		// Future expiry still active.
		idFuture := uuid.New()
		exp := now.Add(1 * time.Hour)
		hash = sha256Of([]byte("rk_future_exp"))
		mustInsert(t, ctx, tables, keys, persistence.APIKey{
			ID: idFuture, KeyHash: hash[:], Name: "future-exp",
			Permissions: []byte(`[{"action":"*"}]`), CreatedAt: now, ExpiresAt: &exp,
		})
		n, err = keys.ActiveCount(ctx, now, nil)
		if err != nil || n != 2 {
			t.Fatalf("ActiveCount after future-exp: n=%d err=%v", n, err)
		}
		// Past expiry: not active.
		n, err = keys.ActiveCount(ctx, now.Add(2*time.Hour), nil)
		if err != nil || n != 1 {
			t.Fatalf("ActiveCount after expiry passes: n=%d err=%v", n, err)
		}
		// In-grace (revoke_at in the future) is still active.
		idGrace := uuid.New()
		future := now.Add(1 * time.Hour)
		hash = sha256Of([]byte("rk_in_grace"))
		mustInsert(t, ctx, tables, keys, persistence.APIKey{
			ID: idGrace, KeyHash: hash[:], Name: "in-grace",
			Permissions: []byte(`[{"action":"*"}]`), CreatedAt: now, RevokeAt: &future,
		})
		n, err = keys.ActiveCount(ctx, now, nil)
		if err != nil || n != 3 {
			t.Fatalf("ActiveCount with in-grace: n=%d err=%v", n, err)
		}
		// Past-grace: revoke_at <= now → not active.
		n, err = keys.ActiveCount(ctx, future.Add(time.Second), nil)
		if err != nil || n != 1 {
			t.Fatalf("ActiveCount past-grace: n=%d err=%v", n, err)
		}
	})

	// MarkRevoked is idempotent and distinguishes (changed, found)
	// so handleRevokeKey can avoid emitting a duplicate
	// auth.key_revoked when a prior caller (typically the rotation-
	// grace sweep) revoked first.
	t.Run("MarkRevoked_Idempotent", func(t *testing.T) {
		id := uuid.New()
		hash := sha256Of([]byte("rk_idem"))
		mustInsert(t, ctx, tables, keys, persistence.APIKey{
			ID: id, KeyHash: hash[:], Name: "idem-revoke",
			Permissions: []byte(`[{"action":"*"}]`), CreatedAt: now,
		})
		// First call: mutates the row → (changed=true, found=true).
		changed, found, err := keys.MarkRevoked(ctx, id, now, nil)
		if err != nil || !changed || !found {
			t.Fatalf("MarkRevoked first: changed=%v found=%v err=%v (want true,true,nil)", changed, found, err)
		}
		// Second call: row exists but already revoked → (changed=false, found=true).
		changed, found, err = keys.MarkRevoked(ctx, id, now, nil)
		if err != nil || changed || !found {
			t.Fatalf("MarkRevoked second (already revoked): changed=%v found=%v err=%v (want false,true,nil)", changed, found, err)
		}
		// Nonexistent row → (changed=false, found=false, err=nil).
		changed, found, err = keys.MarkRevoked(ctx, uuid.New(), now, nil)
		if err != nil || changed || found {
			t.Fatalf("MarkRevoked missing: changed=%v found=%v err=%v (want false,false,nil)", changed, found, err)
		}
	})

	// SetRevokeAt + SweepRotationGrace.
	t.Run("SweepRotationGrace", func(t *testing.T) {
		id := uuid.New()
		hash := sha256Of([]byte("rk_sweep"))
		mustInsert(t, ctx, tables, keys, persistence.APIKey{
			ID: id, KeyHash: hash[:], Name: "to-sweep",
			Permissions: []byte(`[{"action":"*"}]`), CreatedAt: now,
		})
		past := now.Add(-1 * time.Minute)
		if err := keys.SetRevokeAt(ctx, id, past, nil); err != nil {
			t.Fatalf("SetRevokeAt: %v", err)
		}
		swept, err := keys.SweepRotationGrace(ctx, now, nil)
		if err != nil {
			t.Fatalf("Sweep: %v", err)
		}
		found := false
		for _, k := range swept {
			if k.ID == id {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected swept to include id; got %+v", swept)
		}
		// Idempotent re-sweep.
		swept2, err := keys.SweepRotationGrace(ctx, now, nil)
		if err != nil {
			t.Fatalf("Sweep2: %v", err)
		}
		for _, k := range swept2 {
			if k.ID == id {
				t.Fatalf("re-sweep should not return already-revoked row")
			}
		}
		// Verify the row is now revoked.
		got, _, err := keys.GetByID(ctx, id, nil)
		if err != nil || got.RevokedAt == nil {
			t.Fatalf("post-sweep row: err=%v revoked_at=%v", err, got.RevokedAt)
		}
	})

	// Unique-name partial index.
	t.Run("UniqueNameDuringRotation", func(t *testing.T) {
		// Two active inserts with the same name → second errors.
		hash1 := sha256Of([]byte("rk_dup_a"))
		hash2 := sha256Of([]byte("rk_dup_b"))
		mustInsert(t, ctx, tables, keys, persistence.APIKey{
			ID: uuid.New(), KeyHash: hash1[:], Name: "dup",
			Permissions: []byte(`[{"action":"*"}]`), CreatedAt: now,
		})
		err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return keys.Insert(ctx, persistence.APIKey{
				ID: uuid.New(), KeyHash: hash2[:], Name: "dup",
				Permissions: []byte(`[{"action":"*"}]`), CreatedAt: now,
			}, tx)
		})
		if !errors.Is(err, persistence.ErrAPIKeyNameTaken) {
			t.Fatalf("expected ErrAPIKeyNameTaken; got %v", err)
		}
		// Rotation flow: set revoke_at on the first row, then insert
		// the second — should succeed (the first drops out of the
		// partial unique-name index).
		oldID := getActiveIDByName(t, ctx, keys, "dup")
		future := now.Add(1 * time.Hour)
		err = tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			if err := keys.SetRevokeAt(ctx, oldID, future, tx); err != nil {
				return err
			}
			return keys.Insert(ctx, persistence.APIKey{
				ID: uuid.New(), KeyHash: hash2[:], Name: "dup",
				Permissions: []byte(`[{"action":"*"}]`), CreatedAt: now,
			}, tx)
		})
		if err != nil {
			t.Fatalf("rotation flow insert: %v", err)
		}
	})

	// Tx rollback semantics.
	t.Run("Transaction_Rollback", func(t *testing.T) {
		id := uuid.New()
		hash := sha256Of([]byte("rk_rollback"))
		sentinel := errors.New("rollback please")
		err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			if err := keys.Insert(ctx, persistence.APIKey{
				ID: id, KeyHash: hash[:], Name: "rollback-target",
				Permissions: []byte(`[{"action":"*"}]`), CreatedAt: now,
			}, tx); err != nil {
				return err
			}
			return sentinel
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("expected sentinel; got %v", err)
		}
		_, ok, err := keys.GetByID(ctx, id, nil)
		if err != nil {
			t.Fatalf("GetByID after rollback: %v", err)
		}
		if ok {
			t.Fatalf("row must not be visible after rollback")
		}
	})
}

// helpers --------------------------------------------------------------

func sha256Of(b []byte) [32]byte { return sha256.Sum256(b) }

func mustInsert(t *testing.T, ctx context.Context, tables persistence.Tables, keys persistence.APIKeyTable, k persistence.APIKey) {
	t.Helper()
	err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return keys.Insert(ctx, k, tx)
	})
	if err != nil {
		t.Fatalf("Insert(%s): %v", k.Name, err)
	}
}

func mustMarkRevoked(t *testing.T, ctx context.Context, tables persistence.Tables, keys persistence.APIKeyTable, id shared.UUID, now time.Time) {
	t.Helper()
	_, _, err := keys.MarkRevoked(ctx, id, now, nil)
	if err != nil {
		t.Fatalf("MarkRevoked(%s): %v", id, err)
	}
}

func names(rows []persistence.APIKey) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Name
	}
	sort.Strings(out)
	return out
}

func getActiveIDByName(t *testing.T, ctx context.Context, keys persistence.APIKeyTable, name string) shared.UUID {
	t.Helper()
	row, ok, err := keys.GetByName(ctx, name, nil)
	if err != nil || !ok {
		t.Fatalf("GetByName(%q): ok=%v err=%v", name, ok, err)
	}
	return row.ID
}
