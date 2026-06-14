// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// artifact_swap_darwin.go — darwin implementation of the atomic
// inode-swap primitive UpdateLatestSymlink uses on the overwrite path.
// Uses `renamex_np` with RENAME_SWAP, which exchanges two directory
// entries in a single syscall; concurrent readlink against either
// path observes one or the other valid target, never a missing or
// EINVAL-yielding entry.

package compose

import "golang.org/x/sys/unix"

// swapAtomicInodes atomically swaps the directory entries `a` and `b`
// such that the contents (the symlink targets) are exchanged in one
// kernel step. Both paths must exist and be on the same filesystem.
func swapAtomicInodes(a, b string) error {
	return unix.RenamexNp(a, b, unix.RENAME_SWAP)
}
