// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claimproducers

import "testing"

func TestOpenErrorRollsBackRimskySideInsertsDelegated(t *testing.T) {
	t.Skip("delegated to test/scenarios/locks/atomic_acquisition_test.go::" +
		"TestAtomicAcquisitionRollsBackOnOpenError — same property (rimsky-side " +
		"INSERTs must roll back when ClaimProducer.Open errors), exercised via " +
		"foundation/locks/storetest.Fake error injection")
}
