// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package store

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

func (s *Store) RunSweep(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.sweepOnce()
	}
}

func (s *Store) sweepOnce() {
	for selector, pp := range s.pickPolicies {
		if pp.VisibilityTimeout <= 0 {
			continue
		}
		state := PolicyStateDir(s.root, selector)
		inProg := filepath.Join(state, "in_progress")
		avail := filepath.Join(state, "available")
		entries, err := os.ReadDir(inProg)
		if err != nil {
			slog.Warn("filesystem store: sweep readdir", "selector", selector, "error", err.Error())
			continue
		}
		cutoff := time.Now().Add(-pp.VisibilityTimeout).UnixNano()
		reclaimed := false
		for _, e := range entries {
			folder, _, claimedNanos, perr := ParseFromRight(e.Name())
			if perr != nil {
				continue
			}
			if claimedNanos > cutoff {
				continue
			}
			src := filepath.Join(inProg, e.Name())
			folderAbs := filepath.Join(s.root, pp.Root, folder)
			if _, statErr := os.Stat(folderAbs); statErr != nil {
				if err := os.Remove(src); err != nil && !errors.Is(err, fs.ErrNotExist) {
					slog.Warn("filesystem store: sweep unlink orphan sentinel", "selector", selector, "folder", folder, "error", err.Error())
					continue
				}
				fsyncDir(inProg)
				continue
			}
			dst := filepath.Join(avail, folder)
			if err := os.Rename(src, dst); err != nil {
				if !errors.Is(err, fs.ErrNotExist) {
					slog.Warn("filesystem store: sweep reclaim", "selector", selector, "folder", folder, "error", err.Error())
				}
				continue
			}
			fsyncDir(inProg)
			fsyncDir(avail)
			reclaimed = true
		}
		if reclaimed {
			removeDrainedIfPresent(state)
		}
	}
}
