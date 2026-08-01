// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package errorclasses

func Declared() []string {
	return []string{
		"http/network_error",
		"http/timeout",
		"http/request_invalid/*",
		"http/server_error/*",
		"http/expectation_mismatch",
		"http/response_unparseable",
		"http/response_truncated",
		"http/attribute_invalid",
		"http/internal_error",
	}
}
