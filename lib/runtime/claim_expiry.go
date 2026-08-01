// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import "time"

const claimExpiryLivenessMultiplier = 5

func claimExpiryFromLiveness(now time.Time, livenessInterval time.Duration) time.Time {
	return now.Add(claimExpiryLivenessMultiplier * livenessInterval)
}
