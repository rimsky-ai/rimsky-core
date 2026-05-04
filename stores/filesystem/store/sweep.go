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

// RunSweep runs the visibility-timeout sweep + (when SyncStrategy is
// on_sweep) the auto-discovery sync. Returns when ctx is cancelled.
//
// Per spec §7.5 / 2026-05-03-fs-store-pick-policies-design.md
// "Sweep loop": purely store-internal; does not consult
// rimsky_lock_holders.
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
		if pp.SyncStrategy == "on_sweep" {
			if err := s.runSync(selector, pp); err != nil {
				slog.Warn("filesystem store: sweep sync", "selector", selector, "error", err.Error())
			}
		}
		if pp.VisibilityTimeout <= 0 {
			continue
		}
		inProg := filepath.Join(policyStateDir(s.root, selector), "in_progress")
		avail := filepath.Join(policyStateDir(s.root, selector), "available")
		entries, err := os.ReadDir(inProg)
		if err != nil {
			slog.Warn("filesystem store: sweep readdir", "selector", selector, "error", err.Error())
			continue
		}
		cutoff := time.Now().Add(-pp.VisibilityTimeout).UnixNano()
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
			if err := os.Rename(src, dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
				slog.Warn("filesystem store: sweep reclaim", "selector", selector, "folder", folder, "error", err.Error())
			}
		}
	}
	return nil
}
