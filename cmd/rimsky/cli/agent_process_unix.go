// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

//go:build !windows

// agent_process_unix.go — signal-based process probes/teardown for the
// agent daemon lifecycle on unix-like systems. The Windows twin
// (agent_process_windows.go) implements the same three-function surface
// via OpenProcess/TerminateProcess so the CLI builds for every GOOS the
// Makefile release matrix targets.

package cli

import "syscall"

// processAlive reports whether pid names a live process (signal 0 probe).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	// On unix, signal 0 performs error checking without delivering a signal.
	return syscall.Kill(pid, 0) == nil
}

// terminateProcess asks pid to shut down gracefully (SIGTERM).
func terminateProcess(pid int) error {
	return syscall.Kill(pid, syscall.SIGTERM)
}

// killProcess force-kills pid (SIGKILL), best-effort.
func killProcess(pid int) {
	_ = syscall.Kill(pid, syscall.SIGKILL)
}
