// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package claimproducer

import "errors"

// ErrSplitScopeUnsupported is the sentinel returned by ClaimProducer
// implementations that do not advertise Capabilities.SupportsSplitScope.
// Rimsky validates Capabilities at registration and should never call
// SplitScope on producers that do not advertise the capability; this
// sentinel exists so a non-advertising producer can return a clean
// failure when called accidentally.
var ErrSplitScopeUnsupported = errors.New("split_scope unsupported by this producer")

// ErrScopesConflictUnsupported is the sentinel returned by
// ClaimProducer implementations that do not advertise
// Capabilities.SupportsScopesConflict. Rimsky falls back to byte-equal
// comparison when this sentinel is returned per @blessed-invariant 4b.
var ErrScopesConflictUnsupported = errors.New("scopes_conflict unsupported by this producer")

// ErrScopesConflictUnsupportedFallback is the rimsky-internal helper
// that implements the byte-equal fallback. The wire-client uses this
// when the producer does not advertise SupportsScopesConflict so
// callers can route through a single boolean-returning method.
func ErrScopesConflictUnsupportedFallback(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
