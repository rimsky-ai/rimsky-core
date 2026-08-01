// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: api-key
// @concept: permission

package controlapi

import (
	"context"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
)

type ctxKeyIdentity struct{}

type ctxKeyMode struct{}

type ctxKeyProtocolSkin struct{}

func IdentityFromContextOK(ctx context.Context) (auth.Identity, bool) {
	v, ok := ctx.Value(ctxKeyIdentity{}).(auth.Identity)
	return v, ok
}

func ModeFromContext(ctx context.Context) auth.Mode {
	if m, ok := ctx.Value(ctxKeyMode{}).(auth.Mode); ok {
		return m
	}
	return auth.ModeExecute
}

func WithProtocolSkin(ctx context.Context, skin string) context.Context {
	return context.WithValue(ctx, ctxKeyProtocolSkin{}, skin)
}

func protocolSkinFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeyProtocolSkin{}).(string); ok && v != "" {
		return v
	}
	return "http"
}
