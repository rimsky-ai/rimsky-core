// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

//go:build !windows

// advisory_flock_unix.go — flock(2)-based file-lock primitives backing
// the SQLite advisory locker on unix-like systems. The Windows twin
// (advisory_flock_windows.go) implements the same three-function
// surface via LockFileEx so the foundation module builds for every
// GOOS an embedding consumer might target.

package sqlite

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// flockTry opens (creating if absent) the lock file at path and attempts
// a non-blocking exclusive flock on the fresh fd. Returns
// (nil, false, nil) when another holder — in this process or any other —
// already holds the lock.
func flockTry(path string) (*os.File, bool, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, false, fmt.Errorf("sqlite advisory lock: open %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("sqlite advisory lock: flock %s: %w", path, err)
	}
	return f, true, nil
}

// flockRelease unlocks and closes a file returned by flockTry. Closing
// the fd alone releases the flock; the explicit unlock makes the
// release immediate even if a duplicated fd lingers.
func flockRelease(f *os.File) error {
	unlockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	closeErr := f.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
