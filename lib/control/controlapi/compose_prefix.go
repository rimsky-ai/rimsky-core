// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: permission
package controlapi

import (
	"net/http"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
)

const composeReservedPrefix = "compose:"

const composeOriginHeader = "X-Rimsky-Compose-Origin"

const composeOriginAction = "compose:origin"

func isComposeOrigin(r *http.Request) bool {
	if r.Header.Get(composeOriginHeader) != "1" {
		return false
	}
	ident, ok := IdentityFromContextOK(r.Context())
	if !ok {
		return false
	}
	res := auth.CheckGrant(ident.Permissions, composeOriginAction, nil)
	return res.Allowed
}
