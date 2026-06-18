// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package errorclasses

func Declared() []string {
	return []string{
		"verifier/attribute_invalid",
		"verifier/check_failed/*",
	}
}
