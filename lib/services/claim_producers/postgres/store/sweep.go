// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
)

const stagingOrphanHorizon = 24 * time.Hour

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
	var errs []error
	for selector, pp := range s.pickPolicies {
		if pp.VisibilityTimeout <= 0 {
			continue
		}
		q := fmt.Sprintf(
			`UPDATE %s
			    SET state = 'available', claim_token = NULL, claimed_at = NULL,
			        sequence = nextval(pg_get_serial_sequence($1, 'sequence'))
			  WHERE state = 'in_progress'
			    AND claimed_at < now() - ($2 * interval '1 millisecond')`,
			pp.ItemsTable,
		)
		ms := pp.VisibilityTimeout.Milliseconds()
		if _, err := s.pool.Exec(ctx, q, pp.ItemsTable, ms); err != nil {
			slog.Warn("postgres store: sweep one policy failed",
				"selector", selector, "items_table", pp.ItemsTable, "error", err.Error())
			errs = append(errs, fmt.Errorf("sweep %q (%s): %w", selector, pp.ItemsTable, err))
		}
	}
	s.sweepStagingOrphans(ctx)
	return errors.Join(errs...)
}

func (s *Store) sweepStagingOrphans(ctx context.Context) {
	horizonMs := stagingOrphanHorizon.Milliseconds()
	rows, err := s.pool.Query(ctx,
		`DELETE FROM `+stagingReservationsTable+`
		   WHERE reserved_at < now() - ($1 * interval '1 millisecond')
		 RETURNING schema_name`,
		horizonMs)
	if err != nil {
		slog.Warn("postgres store: sweep staging orphans: query failed", "error", err.Error())
		return
	}
	var orphaned []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			slog.Warn("postgres store: sweep staging orphans: scan failed", "error", err.Error())
			continue
		}
		orphaned = append(orphaned, name)
	}
	rowsErr := rows.Err()
	rows.Close()
	if rowsErr != nil {
		slog.Warn("postgres store: sweep staging orphans: iterate failed", "error", rowsErr.Error())
	}
	for _, name := range orphaned {
		if err := requireStagingSchemaIdent(name); err != nil {
			slog.Warn("postgres store: sweep staging orphans: refusing drop", "staging", name, "error", err.Error())
			continue
		}
		if _, err := s.pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{name}.Sanitize()+" CASCADE"); err != nil {
			slog.Warn("postgres store: sweep staging orphans: drop failed", "staging", name, "error", err.Error())
		}
	}
}
