// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package compose

import "golang.org/x/sys/unix"

func swapAtomicInodes(a, b string) error {
	return unix.RenamexNp(a, b, unix.RENAME_SWAP)
}
