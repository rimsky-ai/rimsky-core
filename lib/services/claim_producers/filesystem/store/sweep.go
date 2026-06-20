// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
		if err := s.sweepOnce(); err != nil {
			slog.Warn("filesystem store: sweep", "error", err.Error())
		}
	}
}

func (s *Store) sweepOnce() error {
	for selector, pp := range s.pickPolicies {
		if pp.VisibilityTimeout <= 0 {
			continue
		}
		state := policyStateDir(s.root, selector)
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
			folder, _, claimedNanos, perr := parseFromRight(e.Name())
			if perr != nil {
				continue
			}
			if claimedNanos > cutoff {
				continue
			}
			src := filepath.Join(inProg, e.Name())
			dst := filepath.Join(avail, folder)
			if err := os.Rename(src, dst); err != nil {
				if !errors.Is(err, fs.ErrNotExist) {
					slog.Warn("filesystem store: sweep reclaim", "selector", selector, "folder", folder, "error", err.Error())
				}
				continue
			}
			reclaimed = true
		}
		if reclaimed {
			removeDrainedIfPresent(state)
		}
	}
	return nil
}
