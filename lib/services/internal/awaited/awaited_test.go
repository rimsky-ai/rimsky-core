// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package awaited

import (
	"sync/atomic"
	"testing"
)

func TestUntilReturnsAsSoonAsTheConditionHolds(t *testing.T) {
	polls := 0
	Until(t, "the condition to hold on the first poll", func() bool {
		polls++
		return true
	})
	if polls != 1 {
		t.Fatalf("Until polled %d time(s) for a condition that held immediately", polls)
	}
}

func TestUntilKeepsPollingUntilAConcurrentWriterSatisfiesTheCondition(t *testing.T) {
	var ready atomic.Bool
	started := make(chan struct{})
	go func() {
		<-started
		ready.Store(true)
	}()
	close(started)
	Until(t, "a concurrent writer to publish its result", ready.Load)
	if !ready.Load() {
		t.Fatal("Until returned before the condition held")
	}
}
