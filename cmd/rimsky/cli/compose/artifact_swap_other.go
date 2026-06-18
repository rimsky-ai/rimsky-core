// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

//go:build !darwin && !linux

package compose

import "os"

func swapAtomicInodes(a, b string) error {
	return os.Rename(a, b)
}
