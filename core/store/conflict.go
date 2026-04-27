// Mode-coexistence helper (spec §8.5). Pure; no I/O; deterministic on
// inputs. Lives in core/store/ so both the supervisor's acquisition flow
// (core/supervisor/runner_acquire.go) and the queue's eligibility
// predicate (core/queue/postgres/queue.go) can call it without circular
// imports.
//
// A claim's effective mode is (sync|async, r|w) derived from
// (intent, store.write_semantics) at conflict-check time. The matrix
// is symmetric.

package store

// ModeCoexists reports whether two claims with given intents on stores
// with given write_semantics can coexist on overlapping regions, per the
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
// store_name first). When this helper is called for two claims on the
// same store, semA == semB; the function nonetheless handles the
// cross-quadrant inputs by returning true (no conflict) so callers don't
// need to special-case.
//
// The w×w false in both blocks is the structural single-writer-per-region
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

// isSync reports whether the given write_semantics value is in the sync
// block (direct or staged_blocking). staged_async is the only async value.
func isSync(ws WriteSemantics) bool {
	switch ws {
	case WriteSemanticsDirect, WriteSemanticsStagedBlocking:
		return true
	case WriteSemanticsStagedAsync:
		return false
	}
	// Unrecognized: conservative — treat as sync.
	return true
}
