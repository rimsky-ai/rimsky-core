// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import "time"

const claimExpiryLivenessMultiplier = 5

func claimExpiryFromLiveness(now time.Time, livenessInterval time.Duration) time.Time {
	return now.Add(claimExpiryLivenessMultiplier * livenessInterval)
}
