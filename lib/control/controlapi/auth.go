// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Identity + per-request mode context plumbing for the auth pipeline.
// The outer middleware (IdentityResolver) and per-handler gate
// (gateByAction) live in auth_middleware.go and consult these helpers.
//
// @concept: api-key
// @concept: permission

package controlapi

import (
	"context"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
)

// ctxKeyIdentity is the context key for the resolved Identity.
type ctxKeyIdentity struct{}

// ctxKeyMode is the context key for the per-request Mode resolved from
// the `?dry_run=true` request flag by gateByAction. Read by handlers
// via ModeFromContext to honor dry-run.
type ctxKeyMode struct{}

// ctxKeyProtocolSkin is the context key for the protocol-skin
// originating the request ("http" or "mcp"). Set by the MCP
// catalog before dispatch; defaults to "http" when unset.
type ctxKeyProtocolSkin struct{}

// IdentityFromContext returns the Identity placed in ctx by the auth
// middleware. Panics in handlers that run without the middleware
// (which would be a wiring bug — every handler is gated). Callers
// who need a softer failure mode can use IdentityFromContextOK.
func IdentityFromContext(ctx context.Context) auth.Identity {
	id, ok := IdentityFromContextOK(ctx)
	if !ok {
		panic("controlapi: no identity in context — auth middleware missing?")
	}
	return id
}

// IdentityFromContextOK is the softer-failing form of
// IdentityFromContext.
func IdentityFromContextOK(ctx context.Context) (auth.Identity, bool) {
	v, ok := ctx.Value(ctxKeyIdentity{}).(auth.Identity)
	return v, ok
}

// ModeFromContext returns the per-request Mode (execute or dry_run),
// resolved from the `?dry_run=true` request flag by gateByAction.
// Defaults to execute when the flag is absent or the auth middleware
// didn't run.
func ModeFromContext(ctx context.Context) auth.Mode {
	if m, ok := ctx.Value(ctxKeyMode{}).(auth.Mode); ok {
		return m
	}
	return auth.ModeExecute
}

// WithProtocolSkin returns ctx tagged with the given protocol skin
// (e.g. "mcp"). The auth audit emitters read this back via
// protocolSkinFromContext; default skin is "http".
func WithProtocolSkin(ctx context.Context, skin string) context.Context {
	return context.WithValue(ctx, ctxKeyProtocolSkin{}, skin)
}

// protocolSkinFromContext returns "mcp" if the request was MCP-
// dispatched (set by Catalog.Invoke before forwarding the inner
// request), else "http".
func protocolSkinFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeyProtocolSkin{}).(string); ok && v != "" {
		return v
	}
	return "http"
}
