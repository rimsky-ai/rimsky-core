// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

//go:build !darwin && !linux

package compose

import "os"

func swapAtomicInodes(a, b string) error {
	return os.Rename(a, b)
}
