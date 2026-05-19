// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Rotation-grace sweep for rimsky_api_keys. Runs in cmd:rimsky-scheduler
// alongside other periodic sweeps. Idempotent. Emits one
// auth.key_revoked event per swept row.
//
// @concept: api-key

package runtime

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/fallguy/rimsky/foundation/auth"
	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
)

// SweepRotationGrace revokes API keys whose rotation-grace window has
// expired. Emits auth.key_revoked with reason=rotation_grace for each
// swept row. Returns the swept-row count.
//
// Idempotent: rows whose revoked_at is already set are not selected
// by SweepRotationGrace; re-runs after the first complete pass return
// 0.
//
// In-process callers (e.g. lifecycle tests that drive sweep from
// the same process as control-api) should also invoke
// `*controlapi.AuthState.OnAuthMutation()` after this returns so the
// per-replica anonymous-mode cache reflects the post-sweep key count
// without waiting for the TTL refresh. Production cross-process
// callers (the scheduler) accept the TTL-bounded staleness per the
// spec ("Anonymous-mode cache invalidation" section).
func SweepRotationGrace(
	ctx context.Context,
	tables persistence.Tables,
	clock shared.Clock,
	log shared.Logger,
) (int, error) {
	now := clock.Now()
	swept, err := tables.APIKeys().SweepRotationGrace(ctx, now, nil)
	if err != nil {
		return 0, err
	}
	for _, k := range swept {
		payload := auth.KeyRevokedPayload{
			KeyID:   k.ID,
			KeyName: k.Name,
			Reason:  auth.RevokeReasonRotationGrace,
		}
		emitKeyRevoked(ctx, tables, payload)
		if log != nil {
			log.Info("auth.rotation_grace_revoked", "key_id", k.ID.String(), "key_name", k.Name)
		}
	}
	for _, h := range registeredAuthMutationHooks() {
		h()
	}
	return len(swept), nil
}

// AuthMutationHook is the callback signature consumers register so
// the sweep can drop per-replica caches in-process. controlapi
// registers `*AuthState.OnAuthMutation` here at startup.
type AuthMutationHook = func()

// registeredHook pairs a hook with a monotonically increasing ID so
// unregister can target a specific registration even when multiple
// hooks share the same function value (function equality is
// unreliable in Go; the ID gives a reliable handle).
type registeredHook struct {
	id uint64
	h  AuthMutationHook
}

var (
	authMutationHooksMu sync.RWMutex
	authMutationHooks   []registeredHook
	authMutationHookSeq uint64
)

// RegisterAuthMutationHook adds a callback fired by SweepRotationGrace
// after every successful sweep. Each registered hook fires once per
// sweep regardless of how many rows were swept. Re-registration is
// allowed; ordering is registration-order. Intended for in-process
// composition only.
//
// Returns an unregister closure callers MUST invoke (via t.Cleanup
// for test fixtures) so hooks don't accumulate across runs in
// long-lived processes. Production wiring (StartControlAPI) keeps the
// registration for the life of the process — that's a one-shot.
func RegisterAuthMutationHook(h AuthMutationHook) func() {
	authMutationHooksMu.Lock()
	authMutationHookSeq++
	id := authMutationHookSeq
	authMutationHooks = append(authMutationHooks, registeredHook{id: id, h: h})
	authMutationHooksMu.Unlock()
	return func() {
		authMutationHooksMu.Lock()
		defer authMutationHooksMu.Unlock()
		for i, rh := range authMutationHooks {
			if rh.id == id {
				authMutationHooks = append(authMutationHooks[:i], authMutationHooks[i+1:]...)
				return
			}
		}
	}
}

// registeredAuthMutationHooks returns a snapshot of the current
// hooks under read lock so SweepRotationGrace can iterate without
// holding the write lock.
func registeredAuthMutationHooks() []AuthMutationHook {
	authMutationHooksMu.RLock()
	defer authMutationHooksMu.RUnlock()
	out := make([]AuthMutationHook, len(authMutationHooks))
	for i, rh := range authMutationHooks {
		out[i] = rh.h
	}
	return out
}

// emitKeyRevoked appends one auth.key_revoked event with the supplied
// payload. Best-effort; errors logged but not returned (the row was
// already revoked successfully).
func emitKeyRevoked(ctx context.Context, tables persistence.Tables, p auth.KeyRevokedPayload) {
	data, err := json.Marshal(p)
	if err != nil {
		return
	}
	payloadMap := map[string]any{}
	if err := json.Unmarshal(data, &payloadMap); err != nil {
		return
	}
	_ = tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return tables.Events().Append(ctx, persistence.EventAppendInput{
			Kind:    auth.EventKeyRevoked,
			Payload: payloadMap,
		}, tx)
	})
}
