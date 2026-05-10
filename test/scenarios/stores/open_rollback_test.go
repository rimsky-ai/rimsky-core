// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Open-rollback scenario coverage — invariant 15 (revised v3): `Open`
// fires inside the rimsky-side acquisition transaction, and an Open
// error must roll back the rimsky-side INSERTs (lock-holder row and
// dispatch claim).
//
// This test deliberately delegates to
// `test/scenarios/locks/atomic_acquisition_test.go::TestAtomicAcquisitionRollsBackOnOpenError`
// which exercises the identical property end-to-end through the
// `foundation/locks/storetest.Fake` error-injection knob: it deploys a
// claim-bearing template, drives RunNode with a Fake whose ErrorFunc
// returns on `open`, and asserts (a) zero rimsky_claim_handle rows for
// the node, (b) the dispatch row's claimed_by reverts to NULL, (c)
// exactly one open call was observed.
//
// Folding the two into one test avoids duplicating the testcontainers
// boot cost. The scoping documentation lives here so future readers
// find invariant-15 coverage by file name; the implementation lives
// next to its sibling rollback test.
package stores

import "testing"

// TestOpenErrorRollsBackRimskySideInsertsDelegated explicitly delegates
// to TestAtomicAcquisitionRollsBackOnOpenError. This file exists so
// `grep -rn "invariant 15"` and a "find a test for invariant 15"
// search land in `test/scenarios/stores/` (where invariant-15-related
// scenarios live) and not only in `test/scenarios/locks/`.
func TestOpenErrorRollsBackRimskySideInsertsDelegated(t *testing.T) {
	t.Skip("delegated to test/scenarios/locks/atomic_acquisition_test.go::" +
		"TestAtomicAcquisitionRollsBackOnOpenError — same property (rimsky-side " +
		"INSERTs must roll back when Store.Open errors), exercised via " +
		"foundation/locks/storetest.Fake error injection")
}
