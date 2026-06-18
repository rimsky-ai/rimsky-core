// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package cli

func TruncHash(s string) string {
	if len(s) <= 19 {
		return s
	}
	return s[:19] + "…"
}
