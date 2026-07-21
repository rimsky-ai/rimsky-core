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
	var swept []persistence.APIKey
	err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var txErr error
		swept, txErr = tables.APIKeys().SweepRotationGrace(ctx, now, tx)
		if txErr != nil {
			return txErr
		}
		for _, k := range swept {
			payload := auth.KeyRevokedPayload{
				KeyID:   k.ID,
				KeyName: k.Name,
				Reason:  auth.RevokeReasonRotationGrace,
			}
			if txErr := emitKeyRevoked(ctx, tables, tx, log, payload); txErr != nil {
				return txErr
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	for _, k := range swept {
		if log != nil {
			log.Info("auth.rotation_grace_revoked", "key_id", k.ID.String(), "key_name", k.Name)
		}
	}
	if len(swept) > 0 {
		for _, h := range registeredAuthMutationHooks() {
			h()
		}
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

func emitKeyRevoked(ctx context.Context, tables persistence.Tables, tx persistence.Tx, log shared.Logger, p auth.KeyRevokedPayload) error {
	data, err := json.Marshal(p)
	if err != nil {
		if log != nil {
			log.Error("auth.key_revoked.marshal", "key_id", p.KeyID.String(), "err", err.Error())
		}
		return err
	}
	payloadMap := map[string]any{}
	if err := json.Unmarshal(data, &payloadMap); err != nil {
		if log != nil {
			log.Error("auth.key_revoked.unmarshal", "key_id", p.KeyID.String(), "err", err.Error())
		}
		return err
	}
	if err := tables.Events().Append(ctx, persistence.EventAppendInput{
		Kind:    events.KindAuthKeyRevoked(),
		Payload: payloadMap,
	}, tx); err != nil {
		if log != nil {
			log.Error("auth.key_revoked.append", "key_id", p.KeyID.String(), "err", err.Error())
		}
		return err
	}
	return nil
}
