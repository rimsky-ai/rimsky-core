// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package awaited

import "testing"

func TestUntilPollsUntilTheConditionHoldsAndStopsThere(t *testing.T) {
	polls := 0
	Until(t, "the third poll", func() bool {
		polls++
		return polls == 3
	})
	if polls != 3 {
		t.Fatalf("Until must return on the poll that first reports ready; got %d poll(s)", polls)
	}
}

func TestUntilReturnsWithoutPollingTwiceWhenTheConditionAlreadyHolds(t *testing.T) {
	polls := 0
	Until(t, "a condition that already holds", func() bool {
		polls++
		return true
	})
	if polls != 1 {
		t.Fatalf("Until must return on the first ready poll; got %d poll(s)", polls)
	}
}
