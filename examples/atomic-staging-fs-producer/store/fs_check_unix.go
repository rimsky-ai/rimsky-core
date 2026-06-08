// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

//go:build unix

package store

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// assertSameFilesystem verifies that `a` and `b` live on the same
// filesystem by comparing their `st_dev` device IDs. rename(2)'s
// atomicity guarantee is bounded to a single filesystem; if the two
// paths span filesystems, the Commit swap loses atomicity and a crash
// mid-Commit can leave either path in a torn state.
//
// Unix-only build constraint: the device-id check uses
// `syscall.Stat_t.Dev`, which is POSIX. The reference producer
// targets Unix substrates (POSIX rename, hard-link, owner semantics
// are all assumed in the pattern doc).
func assertSameFilesystem(a, b string) error {
	devA, err := deviceID(a)
	if err != nil {
		return err
	}
	devB, err := deviceID(b)
	if err != nil {
		return err
	}
	if devA != devB {
		return fmt.Errorf(
			"atomic-staging: staging (%s) and canonical (%s) must live on the same filesystem (st_dev=%d vs %d); "+
				"the two-rename Commit swap is only atomic within a single mount point",
			a, b, devA, devB)
	}
	return nil
}

func deviceID(path string) (uint64, error) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("atomic-staging: stat %s: %w", path, err)
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, errors.New("atomic-staging: stat sys not *syscall.Stat_t (non-Unix substrate?)")
	}
	return uint64(sys.Dev), nil
}
