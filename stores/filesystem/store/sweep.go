// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

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

// RunSweep runs the visibility-timeout sweep. Returns when ctx is cancelled.
//
// Per spec §7.5: purely store-internal; does not consult
// rimsky_claim_handles. Reclaimed in-progress sentinels also clear any
// drained sentinel for the policy so on_drain mode picks the
// recently-reclaimed work back up.
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
				// ENOENT: a concurrent terminal RPC removed the in-progress
				// sentinel just before sweep tried to rename it. Nothing
				// was returned to available/, so reclaimed must NOT be
				// set — see spec §5.3 case #2 (sweep returns the sentinel
				// to available/) which only covers the actual-rename
				// path. Setting reclaimed=true on ENOENT would clobber a
				// still-needed drained sentinel and burn an extra
				// Unavailable cycle on the next Open.
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
