// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

func TruncHash(s string) string {
	if len(s) <= 19 {
		return s
	}
	return s[:19] + "…"
}
