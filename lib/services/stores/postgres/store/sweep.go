// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package store

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

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
