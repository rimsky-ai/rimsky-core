// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package auth

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
