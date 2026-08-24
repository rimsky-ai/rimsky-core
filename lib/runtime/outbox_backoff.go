// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import "time"

// @decision: service-delivery-stall-signal
func outboxRetryBackoff(attemptCount int, base, max time.Duration) time.Duration {
	if max < base {
		max = base
	}
	backoff := base
	for i := 1; i < attemptCount; i++ {
		backoff *= 2
		if backoff >= max {
			return max
		}
	}
	if backoff > max {
		return max
	}
	return backoff
}
