// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

//go:build windows

package sqlite

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// flockTry opens (creating if absent) the lock file at path and attempts
// a non-blocking exclusive LockFileEx on the fresh handle. Returns
// (nil, false, nil) when another holder — in this process or any other —
// already holds the lock.
func flockTry(path string) (*os.File, bool, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, false, fmt.Errorf("sqlite advisory lock: open %s: %w", path, err)
	}
	ol := new(windows.Overlapped)
	err = windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, ol)
	if err != nil {
		_ = f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("sqlite advisory lock: LockFileEx %s: %w", path, err)
	}
	return f, true, nil
}

// flockRelease unlocks and closes a file returned by flockTry.
func flockRelease(f *os.File) error {
	ol := new(windows.Overlapped)
	unlockErr := windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, ol)
	closeErr := f.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
