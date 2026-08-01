// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package compose

import "golang.org/x/sys/unix"

func swapAtomicInodes(a, b string) error {
	return unix.RenamexNp(a, b, unix.RENAME_SWAP)
}
