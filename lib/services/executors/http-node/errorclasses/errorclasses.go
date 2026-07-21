// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
