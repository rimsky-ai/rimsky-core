// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package pgtest

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const startSlotCount = 2

const startSlotLockDir = "rimsky-testpg-startlocks"

const startSlotPollInterval = 75 * time.Millisecond

type startSlotHandle struct {
	fd int
}

var startSlotInProcess = make(chan struct{}, startSlotCount)

var startSlotDirOnce sync.Once

var startSlotDirPath string

var startSlotDirErr error

func acquireStartSlot() (*startSlotHandle, error) {
	startSlotInProcess <- struct{}{}
	fd, err := acquireStartSlotFile()
	if err != nil {
		<-startSlotInProcess
		return nil, err
	}
	return &startSlotHandle{fd: fd}, nil
}

func (h *startSlotHandle) release() {
	if h == nil {
		return
	}
	_ = unix.Flock(h.fd, unix.LOCK_UN)
	_ = unix.Close(h.fd)
	<-startSlotInProcess
}

func acquireStartSlotFile() (int, error) {
	dir, err := ensureStartSlotDir()
	if err != nil {
		return -1, err
	}
	for {
		for i := 0; i < startSlotCount; i++ {
			path := filepath.Join(dir, fmt.Sprintf("slot-%d.lock", i))
			fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR, 0o644)
			if err != nil {
				return -1, fmt.Errorf("open slot %d: %w", i, err)
			}
			if lockErr := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); lockErr == nil {
				return fd, nil
			}
			_ = unix.Close(fd)
		}
		time.Sleep(startSlotPollInterval)
	}
}

func ensureStartSlotDir() (string, error) {
	startSlotDirOnce.Do(func() {
		dir := filepath.Join(os.TempDir(), startSlotLockDir)
		startSlotDirErr = os.MkdirAll(dir, 0o755)
		if startSlotDirErr == nil {
			startSlotDirPath = dir
		}
	})
	return startSlotDirPath, startSlotDirErr
}
