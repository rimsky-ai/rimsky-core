// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: api-key

package runtime

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

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

type AuthMutationHook = func()

type registeredHook struct {
	id uint64
	h  AuthMutationHook
}

var (
	authMutationHooksMu sync.RWMutex
	authMutationHooks   []registeredHook
	authMutationHookSeq uint64
)

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

func registeredAuthMutationHooks() []AuthMutationHook {
	authMutationHooksMu.RLock()
	defer authMutationHooksMu.RUnlock()
	out := make([]AuthMutationHook, len(authMutationHooks))
	for i, rh := range authMutationHooks {
		out[i] = rh.h
	}
	return out
}

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
			Kind:    events.KindAuthKeyRevoked(),
			Payload: payloadMap,
		}, tx)
	})
}
