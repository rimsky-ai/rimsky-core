// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

//
// @concept: api-key

package controlapi

import "github.com/go-chi/chi/v5"

func registerAuthRoutes(r chi.Router, deps AppDeps) {
	if deps.AuthState == nil {
		return
	}
	r.Post("/auth/keys",
		deps.AuthState.gateByAction("auth:create", handleCreateKey(deps)))
	r.Get("/auth/keys",
		deps.AuthState.gateByAction("auth:read", handleListKeys(deps)))
	r.Get("/auth/keys/{nameOrID}",
		deps.AuthState.gateByAction("auth:read", handleShowKey(deps)))
	r.Delete("/auth/keys/{nameOrID}",
		deps.AuthState.gateByAction("auth:revoke", handleRevokeKey(deps)))
	r.Post("/auth/keys/{nameOrID}/rotate",
		deps.AuthState.gateByAction("auth:rotate", handleRotateKey(deps)))
	r.Get("/auth/status",
		deps.AuthState.gateByAction("auth:read", handleAuthStatus(deps)))
}
