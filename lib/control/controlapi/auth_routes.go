// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: api-key

package controlapi

import "github.com/go-chi/chi/v5"

func registerAuthRoutes(r chi.Router, deps AppDeps) {
	r.Post("/auth/keys",
		gate(deps, "auth:create", handleCreateKey(deps)))
	r.Get("/auth/keys",
		gate(deps, "auth:read", handleListKeys(deps)))
	r.Get("/auth/keys/{nameOrID}",
		gate(deps, "auth:read", handleShowKey(deps)))
	r.Delete("/auth/keys/{nameOrID}",
		gate(deps, "auth:revoke", handleRevokeKey(deps)))
	r.Post("/auth/keys/{nameOrID}/rotate",
		gate(deps, "auth:rotate", handleRotateKey(deps)))
	r.Get("/auth/status",
		gate(deps, "auth:read", handleAuthStatus(deps)))
	r.Get("/auth/whoami", handleWhoAmI())
}
