// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package conformance

import (
	"sync/atomic"
	"testing"
	"time"
)

const pollInterval = 25 * time.Millisecond

func pollUntil(t *testing.T, awaited string, ready func() bool) {
	t.Helper()
	for poll := 1; ; poll++ {
		if ready() {
			return
		}
		if poll == 1 {
			t.Logf("pollUntil: waiting for %s — blocks until the condition holds and says so once, so a wait "+
				"that never ends leaves the test guard's no-progress watchdog free to trip", awaited)
		}
		//nolint:testwallclock-outcome inter-poll cadence; this loop returns only when ready() reports success
		time.Sleep(pollInterval)
	}
}

func TestPollUntilReturnsAsSoonAsTheConditionHolds(t *testing.T) {
	polls := 0
	pollUntil(t, "the condition to hold on the first poll", func() bool {
		polls++
		return true
	})
	if polls != 1 {
		t.Fatalf("pollUntil polled %d time(s) for a condition that held immediately", polls)
	}
}

func TestPollUntilKeepsPollingUntilAConcurrentWriterSatisfiesTheCondition(t *testing.T) {
	var ready atomic.Bool
	started := make(chan struct{})
	go func() {
		<-started
		ready.Store(true)
	}()
	close(started)
	pollUntil(t, "a concurrent writer to publish its result", ready.Load)
	if !ready.Load() {
		t.Fatal("pollUntil returned before the condition held")
	}
}
