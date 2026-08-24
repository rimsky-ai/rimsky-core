// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package service

import (
	"testing"
	"time"
)

func TestRenewalDeadlineAtTwoThirdsTTL(t *testing.T) {
	notBefore := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	ttl := 24 * time.Hour
	notAfter := notBefore.Add(ttl)
	got := RenewalDeadline(notBefore, notAfter)
	want := notBefore.Add(16 * time.Hour)
	if !got.Equal(want) {
		t.Fatalf("RenewalDeadline: got %v want %v", got, want)
	}
}

func TestShouldRenewCrossesAtDeadline(t *testing.T) {
	notBefore := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	notAfter := notBefore.Add(24 * time.Hour)
	deadline := RenewalDeadline(notBefore, notAfter)

	cases := []struct {
		name string
		now  time.Time
		want bool
	}{
		{"before deadline", deadline.Add(-time.Nanosecond), false},
		{"exactly at deadline", deadline, true},
		{"after deadline", deadline.Add(time.Nanosecond), true},
		{"well before", notBefore.Add(time.Hour), false},
		{"past expiry", notAfter.Add(time.Hour), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShouldRenew(notBefore, notAfter, tc.now); got != tc.want {
				t.Fatalf("ShouldRenew(%v): got %v want %v", tc.now, got, tc.want)
			}
		})
	}
}

func TestHolderNeedsRenewalWhenEmpty(t *testing.T) {
	h := NewIdentityHolder()
	if !h.NeedsRenewal(time.Now()) {
		t.Fatalf("empty holder must always need renewal")
	}
	if h.HasIdentity() {
		t.Fatalf("empty holder must not report an identity")
	}
}
