// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

//go:build unix

package store

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

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
