// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package errorclasses

func Declared() []string {
	return []string{
		"verifier/attribute_invalid",
		"verifier/network_error",
		"verifier/timeout",
		"verifier/check_failed",
		"verifier/check_failed/*",
	}
}
