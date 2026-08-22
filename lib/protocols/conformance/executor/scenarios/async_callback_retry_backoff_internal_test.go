// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package scenarios

import (
	"strings"
	"testing"
	"time"
)

func attemptsAt(gaps ...time.Duration) []time.Time {
	base := time.Unix(0, 0)
	out := []time.Time{base}
	for _, g := range gaps {
		base = base.Add(g)
		out = append(out, base)
	}
	return out
}

// @concept: executor
func TestCheckRetryBackoff_RejectsAFixedRetryCadence(t *testing.T) {
	err := checkRetryBackoff(attemptsAt(10*time.Millisecond, 10*time.Millisecond))
	if err == nil {
		t.Fatal("a callback retried on a fixed cadence must fail the backoff check")
	}
	if !strings.Contains(err.Error(), "backoff") {
		t.Fatalf("the failure should name the missing backoff, got: %v", err)
	}
}

// @concept: executor
func TestCheckRetryBackoff_RejectsAShrinkingGap(t *testing.T) {
	if err := checkRetryBackoff(attemptsAt(80*time.Millisecond, 10*time.Millisecond)); err == nil {
		t.Fatal("a retry cadence that tightens must fail the backoff check")
	}
}

// @concept: executor
func TestCheckRetryBackoff_RejectsTooFewAttempts(t *testing.T) {
	if err := checkRetryBackoff(attemptsAt(10 * time.Millisecond)); err == nil {
		t.Fatal("two attempts do not show a cadence; the check must say so rather than pass")
	}
}

// @concept: executor
func TestCheckRetryBackoff_AcceptsAWideningCadence(t *testing.T) {
	if err := checkRetryBackoff(attemptsAt(10*time.Millisecond, 30*time.Millisecond, 90*time.Millisecond)); err != nil {
		t.Fatalf("an exponentially widening retry cadence must pass, got: %v", err)
	}
}
