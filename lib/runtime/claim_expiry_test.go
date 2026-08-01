// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"testing"
	"time"
)

func TestClaimExpiryFromLiveness(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	got := claimExpiryFromLiveness(now, 30*time.Second)
	want := now.Add(5 * 30 * time.Second)
	if !got.Equal(want) {
		t.Fatalf("claimExpiryFromLiveness(now, 30s) = %v; want %v", got, want)
	}
}
