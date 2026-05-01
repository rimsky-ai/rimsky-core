package store

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// RunSweep starts the store-internal visibility-timeout sweep.
// Store-internal sweep — runs over the store's own data only,
// by design. v3 spec §7.5 makes this independent of rimsky's
// `rimsky_lock_holders` orphan reaper; no cross-database join is
// attempted (the store's pool may be on a separate database from
// rimsky's control plane).
//
// Operator timing constraint: set `visibility_timeout > 5 ×
// heartbeat_interval` so the store cannot strip a healthy claim
// while rimsky still believes it holds. If `visibility_timeout` is
// shorter, a healthy supervisor whose lock-holder row is still
// refreshing within the orphan-reap window may have its store
// claim torn down out from under it. See docs/operator-guide.md.
//
// Returns when ctx is cancelled.
func (s *Store) RunSweep(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if err := s.sweepOnce(ctx); err != nil {
			slog.Warn("postgres store: sweep failed", "error", err.Error())
		}
	}
}

func (s *Store) sweepOnce(ctx context.Context) error {
	for selector, pp := range s.pickPolicies {
		if pp.VisibilityTimeout <= 0 {
			continue
		}
		// The "NOT EXISTS rimsky_lock_holders" predicate is dropped
		// because the store's pool may be separate from rimsky's
		// control-plane DB — and even when colocated, the store
		// owns its own state. Per spec §7.5, the orphan reaper runs
		// in rimsky and deletes lock-holder rows; the store's
		// sweep is independent.
		q := fmt.Sprintf(
			`UPDATE %s
			    SET state = 'available', claim_token = NULL, claimed_at = NULL
			  WHERE state = 'in_progress'
			    AND claimed_at < now() - ($1 * interval '1 second')`,
			pp.ItemsTable,
		)
		secs := int(pp.VisibilityTimeout / time.Second)
		if _, err := s.pool.Exec(ctx, q, secs); err != nil {
			slog.Warn("postgres store: sweep one policy failed",
				"selector", selector, "items_table", pp.ItemsTable, "error", err.Error())
		}
	}
	return nil
}
