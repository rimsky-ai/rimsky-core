// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Placeholder package for the locks scenario suite under the stores
// redesign.
//
// Pre-redesign tests in this directory exercised the deleted
// store.LockSpec / RegionLockSpec / ClaimLockSpec / LockHandle /
// AcquireLock / OpenHandle / ReleaseLock surface. Under the redesign
// the supervisor's acquisition path no longer exposes these types
// externally — Locks live as locks.NamedLockSpec | claimproducer.ClaimSpec,
// and the verbs are Open / Commit / Abandon / Release on
// store.Store itself.
//
// New scenarios for this directory drive the supervisor through
// scenario.Harness + scenario.MakeNode helpers (named_locks: config
// drives the limit; templates reference by name only). Coverage
// targets: blessed invariants 2, 3, 4, 6, 9a, 10 (invariant 14 is
// retired in v3).

package locks
