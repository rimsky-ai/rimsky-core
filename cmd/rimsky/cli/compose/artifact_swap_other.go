// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

//go:build !darwin && !linux

// artifact_swap_other.go — fallback implementation of the atomic
// inode-swap primitive for platforms that do not expose RENAME_SWAP /
// RENAME_EXCHANGE. The fallback uses plain os.Rename, which closes the
// broken-window race on Linux/darwin only when the swap primitive is
// available; on other platforms the rename(2) semantics are honored
// as POSIX specifies — atomic at the inode level, with the directory
// entry pointing at the staged inode after the call returns.

package compose

import "os"

// swapAtomicInodes makes the directory entry at `b` resolve to the
// symlink staged at `a`. The fallback is plain os.Rename — the
// caller's invariant (no broken-window) holds to whatever degree the
// host kernel's rename atomicity provides.
func swapAtomicInodes(a, b string) error {
	return os.Rename(a, b)
}
