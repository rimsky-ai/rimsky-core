// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: api-key

package runtime

import (
	"context"
	"sync"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/eventpayload"
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
			if txErr := emitKeyRevoked(ctx, tables, log, payload, tx); txErr != nil {
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
			log.Info("AUTH.ROTATIONGRACE.REVOKED", "key_id", k.ID.String(), "key_name", k.Name)
		}
	}
	if len(swept) > 0 {
		for _, h := range registeredAuthMutationHooks() {
			h()
		}
	}
	return len(swept), nil
}

// @concept: api-key
func SweepKeyExpiry(
	ctx context.Context,
	tables persistence.Tables,
	clock shared.Clock,
	log shared.Logger,
) (int, error) {
	now := clock.Now()
	var expired []persistence.APIKey
	err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var txErr error
		expired, txErr = tables.APIKeys().SweepExpired(ctx, now, tx)
		if txErr != nil {
			return txErr
		}
		for _, k := range expired {
			expiresAt := now
			if k.ExpiresAt != nil {
				expiresAt = *k.ExpiresAt
			}
			payload := auth.KeyExpiredPayload{
				KeyID:     k.ID,
				KeyName:   k.Name,
				ExpiresAt: expiresAt,
			}
			if txErr := emitKeyExpired(ctx, tables, log, payload, tx); txErr != nil {
				return txErr
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	for _, k := range expired {
		if log != nil {
			log.Info("AUTH.KEY.EXPIRED", "key_id", k.ID.String(), "key_name", k.Name)
		}
	}
	if len(expired) > 0 {
		for _, h := range registeredAuthMutationHooks() {
			h()
		}
	}
	return len(expired), nil
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

func emitKeyRevoked(ctx context.Context, tables persistence.Tables, log shared.Logger, p auth.KeyRevokedPayload, tx persistence.Tx) error {
	if err := tables.Events().Append(ctx, persistence.EventAppendInput{
		Kind:    events.KindAuthKeyRevoked(),
		Payload: eventpayload.New(auth.KeyRevokedProto(p)),
	}, tx); err != nil {
		if log != nil {
			log.Error("AUTH.KEYREVOKEDEVENT.APPENDFAILED", "key_id", p.KeyID.String(), "err", err.Error())
		}
		return err
	}
	return nil
}

func emitKeyExpired(ctx context.Context, tables persistence.Tables, log shared.Logger, p auth.KeyExpiredPayload, tx persistence.Tx) error {
	if err := tables.Events().Append(ctx, persistence.EventAppendInput{
		Kind:    events.KindAuthKeyExpired(),
		Payload: eventpayload.New(auth.KeyExpiredProto(p)),
	}, tx); err != nil {
		if log != nil {
			log.Error("AUTH.KEYEXPIREDEVENT.APPENDFAILED", "key_id", p.KeyID.String(), "err", err.Error())
		}
		return err
	}
	return nil
}
