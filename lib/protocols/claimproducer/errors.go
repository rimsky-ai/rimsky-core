// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package claimproducer

import "errors"

var ErrSplitScopeUnsupported = errors.New("split_scope unsupported by this producer")

var ErrScopesConflictUnsupported = errors.New("scopes_conflict unsupported by this producer")

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
