// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Mode-coexistence helper. Pure; no I/O; deterministic on inputs.
// Lives in foundation/locks/ so both the supervisor's acquisition flow
// (runtime/runner_acquire.go) and the queue's eligibility
// predicate (foundation/persistence/postgres/queue.go) can call it without
// circular imports.
//
// A claim's effective mode is (sync|async, r|w) derived from
// (intent, store.write_semantics) at conflict-check time. The matrix
// is symmetric.
//
// Plus the rimsky-side scope-conflict comparison (per spec §7.7):
// byte-equal on canonical scope bytes. Canonicalization is the
// producer's responsibility, comparison is rimsky's.

package locks

import "bytes"

// ModeCoexists reports whether two claims with given intents on stores
// with given write_semantics can coexist on overlapping scopes, per the
// spec §8.5 matrix.
//
//	          | sync-r | sync-w | async-r | async-w
//	-----------|--------|--------|---------|--------
//	  sync-r   |   ✅   |   ❌   |  (n/a)  |  (n/a)
//	  sync-w   |   ❌   |   ❌   |  (n/a)  |  (n/a)
//	  async-r  |  (n/a) |  (n/a) |    ✅   |    ✅
//	  async-w  |  (n/a) |  (n/a) |    ✅   |    ❌
//
// Two claims on the same store share its write_semantics — cross-quadrant
// cells are unreachable in normal acquisition (the caller filters by
// producer_name first). When this helper is called for two claims on the
// same store, semA == semB; the function nonetheless handles the
// cross-quadrant inputs by returning true (no conflict) so callers don't
// need to special-case.
//
// The w×w false in both blocks is the structural single-writer-per-scope
// rule.
func ModeCoexists(intentA Intent, semA WriteSemantics, intentB Intent, semB WriteSemantics) bool {
	syncA := isSync(semA)
	syncB := isSync(semB)
	if syncA != syncB {
		// Cross-quadrant: the (semantics, intent) blocks are independent.
		// Reachable only if a caller compares two claims from different
		// stores, which the upstream filter usually rules out — return
		// true to indicate "no semantic conflict" rather than blocking.
		return true
	}
	rwA := intentA == IntentReadWrite
	rwB := intentB == IntentReadWrite
	if syncA {
		// Sync block: r×r ✅; r×w / w×r / w×w ❌.
		return !rwA && !rwB
	}
	// Async block: r×r ✅, r×w ✅, w×r ✅, w×w ❌.
	return !(rwA && rwB)
}

// ClaimScopesByteEqual reports whether two store-supplied claim-scope byte
// slices are equal under byte-wise comparison. The rimsky-side
// implementation of the conflict comparison (per spec §7.7) — v2's
// per-producer scope conflict comparison is byte-equal; producers canonicalize
// claim-scope bytes such that byte-equal correctly indicates conflict.
//
// Empty claim-scopes never conflict: an absent claim-scope (e.g. a NamedLockSpec
// row in a scope-keyed scan) cannot collide with another claim-scope.
func ClaimScopesByteEqual(a, b []byte) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	return bytes.Equal(a, b)
}

// isSync reports whether the given write_semantics value is in the sync
// block. Sync and BlockingAsync block on r×rw conflicts (sync block);
// StagedAsync does not (async block); ReadOnly cannot mutate so the
// matrix degenerates — treat ReadOnly as sync (r-only claims trivially
// coexist with any other r-only claim and never conflict on w).
func isSync(ws WriteSemantics) bool {
	switch ws {
	case WriteSemanticsSync, WriteSemanticsBlockingAsync, WriteSemanticsReadOnly:
		return true
	case WriteSemanticsStagedAsync:
		return false
	}
	// Unrecognized: conservative — treat as sync.
	return true
}
