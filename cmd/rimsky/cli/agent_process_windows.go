// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

//go:build windows

// agent_process_windows.go — process probes/teardown for the agent
// daemon lifecycle on Windows. Mirror of the unix signal-based twin
// (agent_process_unix.go). Windows has no SIGTERM delivery for
// unrelated processes, so "graceful terminate" degrades to
// TerminateProcess — the daemon's reap loop is therefore unix-only
// behavior; on Windows stop is immediate.

package cli

import "golang.org/x/sys/windows"

// processAlive reports whether pid names a live process.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	const stillActive = 259
	return code == stillActive
}

// terminateProcess stops pid. No SIGTERM equivalent exists for an
// unrelated process on Windows, so this is a hard TerminateProcess.
func terminateProcess(pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	return windows.TerminateProcess(h, 1)
}

// killProcess force-kills pid, best-effort.
func killProcess(pid int) {
	_ = terminateProcess(pid)
}
