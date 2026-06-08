// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// compose_prefix.go — server-side enforcement of the reserved `compose:`
// tag / instance_key prefix. The compose command manages the
// `compose:<project>:<...>` namespace; the prefix is the source-of-truth
// reservation, enforced HERE (at the control-api, where rows are
// created) rather than as a CLI courtesy. Tag-create and instance-create
// reject a `compose:`-prefixed name from any caller EXCEPT the privileged
// compose path, which stamps a trusted compose-origin marker on its
// requests. Resolves the (now-resolved) compose-prefix-client-side
// tension.
package controlapi

import "net/http"

// composeReservedPrefix is the reserved tag / instance_key namespace
// owned by the compose command. A name beginning with this prefix may
// only be created by a compose-origin request (see isComposeOrigin).
const composeReservedPrefix = "compose:"

// composeOriginHeader is the trusted marker the compose engine stamps on
// its writes (value "1") so the server guard can distinguish a
// compose-originated write — which legitimately owns the reserved prefix
// — from a foreign one.
const composeOriginHeader = "X-Rimsky-Compose-Origin"

// isComposeOrigin reports whether the request carries the trusted
// compose-origin marker. Only compose-origin requests are permitted to
// create `compose:`-prefixed tags / instance_keys.
func isComposeOrigin(r *http.Request) bool {
	return r.Header.Get(composeOriginHeader) == "1"
}
