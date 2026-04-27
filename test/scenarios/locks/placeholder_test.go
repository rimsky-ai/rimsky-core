// Placeholder package for the locks scenario suite under
// stores-redesign-v2.
//
// The pre-v2 tests in this directory exercised the deleted
// store.LockSpec / RegionLockSpec / ClaimLockSpec / LockHandle /
// AcquireLock / OpenHandle / ReleaseLock surface. Under
// stores-redesign-v2 the supervisor's acquisition path no longer
// exposes these types externally — Locks live as
// store.NamedLockSpec | store.ClaimSpec, and the verbs are
// Open / Commit / Abandon / Delete / Release on store.Store itself.
//
// New scenarios for this directory belong here but must drive the
// supervisor through scenario.Harness + scenario.MakeNode helpers
// (named_locks: config drives the limit; templates reference by name
// only). Coverage targets: blessed invariants 2, 3, 4, 6, 9a, 10, 14.

package locks
