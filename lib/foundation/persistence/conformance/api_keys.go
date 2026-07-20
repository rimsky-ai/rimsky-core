// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func testAPIKeys(t *testing.T, d persistence.Database) {
	t.Helper()
	ctx := context.Background()
	tables := d.Tables()
	keys := tables.APIKeys()

	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)

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
		mustMarkRevoked(t, ctx, tables, keys, id, now)
		_, ok, err = keys.GetByName(ctx, "active-only", nil)
		if err != nil {
			t.Fatalf("GetByName after revoke: %v", err)
		}
		if ok {
			t.Fatalf("GetByName must return ok=false for revoked row")
		}
	})

	t.Run("GetByHash_AllStatuses", func(t *testing.T) {
		id := uuid.New()
		hash := sha256Of([]byte("rk_test_byhash"))
		mustInsert(t, ctx, tables, keys, persistence.APIKey{
			ID: id, KeyHash: hash[:], Name: "by-hash",
			Permissions: []byte(`[{"action":"*"}]`), CreatedAt: now,
		})
		got, ok, err := keys.GetByHash(ctx, hash[:], nil)
		if err != nil || !ok || got.ID != id {
			t.Fatalf("GetByHash active: err=%v ok=%v", err, ok)
		}
		mustMarkRevoked(t, ctx, tables, keys, id, now)
		got, ok, err = keys.GetByHash(ctx, hash[:], nil)
		if err != nil || !ok || got.RevokedAt == nil {
			t.Fatalf("GetByHash revoked: err=%v ok=%v revoked_at=%v", err, ok, got.RevokedAt)
		}
	})

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
		got, err := keys.List(ctx, false, "alpha*", nil)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 1 || got[0].Name != "alpha" {
			t.Fatalf("List excl-revoked: got %d rows (%+v)", len(got), names(got))
		}
		got, err = keys.List(ctx, true, "alpha*", nil)
		if err != nil {
			t.Fatalf("List incl-revoked: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("List incl-revoked: got %d rows (%+v)", len(got), names(got))
		}
	})

	t.Run("ActiveCount", func(t *testing.T) {
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
		n, err = keys.ActiveCount(ctx, now.Add(2*time.Hour), nil)
		if err != nil || n != 1 {
			t.Fatalf("ActiveCount after expiry passes: n=%d err=%v", n, err)
		}
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
		n, err = keys.ActiveCount(ctx, future.Add(time.Second), nil)
		if err != nil || n != 1 {
			t.Fatalf("ActiveCount past-grace: n=%d err=%v", n, err)
		}
	})

	t.Run("MarkRevoked_Idempotent", func(t *testing.T) {
		id := uuid.New()
		hash := sha256Of([]byte("rk_idem"))
		mustInsert(t, ctx, tables, keys, persistence.APIKey{
			ID: id, KeyHash: hash[:], Name: "idem-revoke",
			Permissions: []byte(`[{"action":"*"}]`), CreatedAt: now,
		})
		changed, found, err := keys.MarkRevoked(ctx, id, now, nil)
		if err != nil || !changed || !found {
			t.Fatalf("MarkRevoked first: changed=%v found=%v err=%v (want true,true,nil)", changed, found, err)
		}
		changed, found, err = keys.MarkRevoked(ctx, id, now, nil)
		if err != nil || changed || !found {
			t.Fatalf("MarkRevoked second (already revoked): changed=%v found=%v err=%v (want false,true,nil)", changed, found, err)
		}
		changed, found, err = keys.MarkRevoked(ctx, uuid.New(), now, nil)
		if err != nil || changed || found {
			t.Fatalf("MarkRevoked missing: changed=%v found=%v err=%v (want false,false,nil)", changed, found, err)
		}
	})

	t.Run("RevokeIfNotLast", func(t *testing.T) {
		all, err := keys.List(ctx, true, "", nil)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, k := range all {
			if k.RevokedAt == nil {
				mustMarkRevoked(t, ctx, tables, keys, k.ID, now)
			}
		}

		soleID := uuid.New()
		soleHash := sha256Of([]byte("rk_guard_sole"))
		mustInsert(t, ctx, tables, keys, persistence.APIKey{
			ID: soleID, KeyHash: soleHash[:], Name: "guard-sole",
			Permissions: []byte(`[{"action":"*"}]`), CreatedAt: now,
		})
		if res, err := keys.RevokeIfNotLast(ctx, soleID, now, false, nil); err != nil || res != persistence.RevokeResultWouldLeaveNoneActive {
			t.Fatalf("revoke sole active without force: res=%v err=%v (want WouldLeaveNoneActive)", res, err)
		}
		if n, err := keys.ActiveCount(ctx, now, nil); err != nil || n != 1 {
			t.Fatalf("sole key must survive refused revoke: active=%d err=%v", n, err)
		}

		secondID := uuid.New()
		secondHash := sha256Of([]byte("rk_guard_second"))
		mustInsert(t, ctx, tables, keys, persistence.APIKey{
			ID: secondID, KeyHash: secondHash[:], Name: "guard-second",
			Permissions: []byte(`[{"action":"*"}]`), CreatedAt: now,
		})
		if res, err := keys.RevokeIfNotLast(ctx, secondID, now, false, nil); err != nil || res != persistence.RevokeResultRevoked {
			t.Fatalf("revoke non-last active: res=%v err=%v (want Revoked)", res, err)
		}
		if res, err := keys.RevokeIfNotLast(ctx, soleID, now, false, nil); err != nil || res != persistence.RevokeResultWouldLeaveNoneActive {
			t.Fatalf("revoke now-last active without force: res=%v err=%v (want WouldLeaveNoneActive)", res, err)
		}
		if res, err := keys.RevokeIfNotLast(ctx, soleID, now, true, nil); err != nil || res != persistence.RevokeResultRevoked {
			t.Fatalf("force revoke last active: res=%v err=%v (want Revoked)", res, err)
		}
		if res, err := keys.RevokeIfNotLast(ctx, soleID, now, false, nil); err != nil || res != persistence.RevokeResultAlreadyRevoked {
			t.Fatalf("revoke already-revoked: res=%v err=%v (want AlreadyRevoked)", res, err)
		}
		if res, err := keys.RevokeIfNotLast(ctx, uuid.New(), now, false, nil); err != nil || res != persistence.RevokeResultNotFound {
			t.Fatalf("revoke missing: res=%v err=%v (want NotFound)", res, err)
		}
	})

	t.Run("SweepRotationGrace", func(t *testing.T) {
		id := uuid.New()
		hash := sha256Of([]byte("rk_sweep"))
		mustInsert(t, ctx, tables, keys, persistence.APIKey{
			ID: id, KeyHash: hash[:], Name: "to-sweep",
			Permissions: []byte(`[{"action":"*"}]`), CreatedAt: now,
		})
		past := now.Add(-1 * time.Minute)
		if setFound, err := keys.SetRevokeAt(ctx, id, past, nil); err != nil || !setFound {
			t.Fatalf("SetRevokeAt: found=%v err=%v", setFound, err)
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
		swept2, err := keys.SweepRotationGrace(ctx, now, nil)
		if err != nil {
			t.Fatalf("Sweep2: %v", err)
		}
		for _, k := range swept2 {
			if k.ID == id {
				t.Fatalf("re-sweep should not return already-revoked row")
			}
		}
		got, _, err := keys.GetByID(ctx, id, nil)
		if err != nil || got.RevokedAt == nil {
			t.Fatalf("post-sweep row: err=%v revoked_at=%v", err, got.RevokedAt)
		}
	})

	t.Run("SetRevokeAt_MissingID", func(t *testing.T) {
		setFound, err := keys.SetRevokeAt(ctx, uuid.New(), now, nil)
		if err != nil || setFound {
			t.Fatalf("SetRevokeAt on missing id: found=%v err=%v (want false,nil)", setFound, err)
		}
	})

	t.Run("UniqueNameDuringRotation", func(t *testing.T) {
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
		oldID := getActiveIDByName(t, ctx, keys, "dup")
		future := now.Add(1 * time.Hour)
		err = tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			rotFound, err := keys.SetRevokeAt(ctx, oldID, future, tx)
			if err != nil {
				return err
			}
			if !rotFound {
				t.Fatalf("SetRevokeAt during rotation: id %s not found", oldID)
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

	t.Run("DuplicateIDIsNotMisreportedAsNameTaken", func(t *testing.T) {
		dupID := uuid.New()
		hashA := sha256Of([]byte("rk_dup_id_a"))
		hashB := sha256Of([]byte("rk_dup_id_b"))
		mustInsert(t, ctx, tables, keys, persistence.APIKey{
			ID: dupID, KeyHash: hashA[:], Name: "dup-id-a",
			Permissions: []byte(`[{"action":"*"}]`), CreatedAt: now,
		})
		err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return keys.Insert(ctx, persistence.APIKey{
				ID: dupID, KeyHash: hashB[:], Name: "dup-id-b",
				Permissions: []byte(`[{"action":"*"}]`), CreatedAt: now,
			}, tx)
		})
		if err == nil {
			t.Fatalf("duplicate id insert must error")
		}
		if errors.Is(err, persistence.ErrAPIKeyNameTaken) {
			t.Fatalf("duplicate id misreported as ErrAPIKeyNameTaken: %v", err)
		}
		if errors.Is(err, persistence.ErrAPIKeyHashCollision) {
			t.Fatalf("duplicate id misreported as ErrAPIKeyHashCollision: %v", err)
		}
	})

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
