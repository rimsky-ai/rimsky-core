// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// compose_prefix.go — server-side enforcement of the reserved `compose:`
// tag / instance_key prefix. The compose command manages the
// `compose:<project>:<...>` namespace; the prefix is the source-of-truth
// reservation, enforced HERE (at the control-api, where rows are
// created) rather than as a CLI courtesy. Tag-create and instance-create
// reject a `compose:`-prefixed name from any caller EXCEPT the privileged
// compose path, which BOTH stamps a trusted compose-origin marker on its
// requests AND carries a `compose:origin` permission. Resolves the
// (now-resolved) compose-prefix-client-side tension.
//
// Trust boundary: the header alone is NOT a trust boundary — any
// authenticated caller with tag:create / instance:create could otherwise
// stamp it. The header is a CLAIM of compose-origin; the load-bearing
// check is the `compose:origin` permission, which only the
// compose-CLI's privileged api-key holds. Both must be present:
//   - header alone: the caller is not privileged → reject the reserved
//     name as if no marker was stamped.
//   - permission alone (no header): the caller declined to claim
//     compose-origin → reject; the prefix stays reserved.
//   - header + permission: the privileged compose path is identifying
//     itself; allow the reserved-prefix write.
//
// When no AuthState is wired (in-process tests that pass `AppDeps{}`),
// the gate is unauthenticated; the header alone is honored so the
// route-only test harness can exercise the compose-origin pass path.
// Production wiring always installs an AuthState; the in-process
// `gate()` shim guarantees auth is enforced before the handler ever
// reads this file's checks.
//
// @concept: permission
package controlapi

import (
	"net/http"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
)

// composeReservedPrefix is the reserved tag / instance_key namespace
// owned by the compose command. A name beginning with this prefix may
// only be created by a privileged compose-origin request — see
// isComposeOrigin (header + permission gate).
const composeReservedPrefix = "compose:"

// composeOriginHeader is the marker the compose engine stamps on its
// writes (value "1") to CLAIM compose-origin. The header alone does
// NOT grant access — the caller must additionally hold the
// `compose:origin` permission. The header is the request-level intent
// signal; the permission is the trust boundary.
const composeOriginHeader = "X-Rimsky-Compose-Origin"

// composeOriginAction is the permission action that gates the
// reserved-prefix bypass. Only api-keys whose grant matches
// `compose:origin` (typically the compose-CLI's privileged key) are
// permitted to create `compose:`-prefixed tags / instance_keys.
const composeOriginAction = "compose:origin"

// isComposeOrigin reports whether the request is a privileged
// compose-origin write — i.e. it stamps the compose-origin header AND
// the authenticated identity holds the `compose:origin` permission.
// Both gates must pass: a CURL with the header but without the
// permission lands on the same reject path as an unmarked request, so
// the prefix reservation actually holds at the source of truth.
//
// In test mode (no Identity on ctx — AuthState not wired), the header
// alone is honored. Production wiring always installs an AuthState.
func isComposeOrigin(r *http.Request) bool {
	if r.Header.Get(composeOriginHeader) != "1" {
		return false
	}
	ident, ok := IdentityFromContextOK(r.Context())
	if !ok {
		// @constraint: no auth middleware ran (in-process route-only test harness).
		// Fall back to header-only for test compatibility; production
		// wires AuthState in NewApp so this branch never executes there.
		return true
	}
	res := auth.CheckGrant(ident.Permissions, composeOriginAction, nil)
	return res.Allowed
}
