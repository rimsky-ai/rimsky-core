// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package auth

// @decision: auth-grant-scope
func ScopeMatches(entryScope map[string]string, target map[string]string) bool {
	if len(entryScope) == 0 {
		return true
	}
	for k, want := range entryScope {
		got, ok := target[k]
		if !ok || got != want {
			return false
		}
	}
	return true
}
