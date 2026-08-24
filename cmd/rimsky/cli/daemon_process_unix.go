// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

//go:build !windows

package cli

import "syscall"

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func terminateProcess(pid int) error {
	return syscall.Kill(pid, syscall.SIGTERM)
}

func killProcess(pid int) {
	_ = syscall.Kill(pid, syscall.SIGKILL)
}
