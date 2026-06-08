// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package auth

// ScopeMatches reports whether a grant entry's resource selector
// (entryScope) is satisfied by a request's resource target (target).
//
// Contract (subset-satisfaction):
//   - An empty or nil entryScope is unscoped: it matches ANY target,
//     including a nil/empty one. (A grant entry that names no scope
//     selector is platform-wide for its action, the pre-scope default.)
//   - A non-empty entryScope is satisfied iff EVERY key in entryScope is
//     present in target with a byte-equal value. Extra keys in target
//     are ignored — the selector constrains only the dimensions it names.
//   - A missing key, or a present key with a differing value, fails the
//     match (least-privilege: an unspecified target dimension is NOT
//     treated as a wildcard match against a named selector key).
//
// The match is exact-string per key; no wildcard grammar applies to
// scope values (the wildcard grammar is action-only, see ActionMatches).
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
