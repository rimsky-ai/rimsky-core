// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package awaited

import (
	"testing"
	"time"
)

const pollInterval = 25 * time.Millisecond

// @decision: polling-audit
// @decision: testing-scenario-based-e2e
func Until(t testing.TB, awaited string, ready func() bool) {
	t.Helper()
	for poll := 1; ; poll++ {
		if ready() {
			return
		}
		if poll == 1 {
			t.Logf("awaited.Until: waiting for %s — blocks until the condition holds and says so once, so a wait "+
				"that never ends leaves the test guard's no-progress watchdog free to trip", awaited)
		}
		//nolint:testwallclock-outcome inter-poll cadence; this loop returns only when ready() reports success
		time.Sleep(pollInterval)
	}
}
