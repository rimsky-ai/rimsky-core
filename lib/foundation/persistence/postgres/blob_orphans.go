// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// blob_orphans.go is the postgres accessor for the rimsky_blob_orphans
// table. The table tracks blob handles whose referencing rows have been
// overwritten or deleted; the SweepOrphanedBlobs sweep deletes rows
// where reap_after <= now() and calls BlobBackend.Delete(handle).
//
// Insert is idempotent on Handle (PK conflict → no-op).
package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

// BlobOrphans returns the postgres BlobOrphanTable impl.
func (s *tablesImpl) BlobOrphans() persistence.BlobOrphanTable {
	return (*blobOrphansImpl)(s)
}

type blobOrphansImpl tablesImpl

func (b *blobOrphansImpl) q(tx persistence.Tx) querier { return (*tablesImpl)(b).q(tx) }

var _ persistence.BlobOrphanTable = (*blobOrphansImpl)(nil)

// Insert queues a handle for orphan reaping. PK conflict on `handle`
// is silently swallowed (the handle is already queued).
func (b *blobOrphansImpl) Insert(ctx context.Context, row persistence.BlobOrphanRow, tx persistence.Tx) error {
	_, err := b.q(tx).Exec(ctx,
		`INSERT INTO rimsky_blob_orphans (handle, backend, orphaned_at, reap_after)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (handle) DO NOTHING`,
		row.Handle, row.Backend, row.OrphanedAt, row.ReapAfter,
	)
	if err != nil {
		return fmt.Errorf("blob_orphans.Insert: %w", err)
	}
	return nil
}

// DueBefore returns rows whose reap_after has passed, ordered ascending,
// up to limit. Used by the SweepOrphanedBlobs sweep.
func (b *blobOrphansImpl) DueBefore(ctx context.Context, cutoff time.Time, limit int) ([]persistence.BlobOrphanRow, error) {
	if limit <= 0 {
		limit = 100
	}
	tx, err := b.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("blob_orphans.DueBefore: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx,
		`SELECT handle, backend, orphaned_at, reap_after
		   FROM rimsky_blob_orphans
		  WHERE reap_after <= $1
		  ORDER BY reap_after ASC
		  LIMIT $2`,
		cutoff, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("blob_orphans.DueBefore: %w", err)
	}
	defer rows.Close()
	var out []persistence.BlobOrphanRow
	for rows.Next() {
		var r persistence.BlobOrphanRow
		if err := rows.Scan(&r.Handle, &r.Backend, &r.OrphanedAt, &r.ReapAfter); err != nil {
			return nil, fmt.Errorf("blob_orphans.DueBefore: scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("blob_orphans.DueBefore: rows.Err: %w", err)
	}
	return out, nil
}

// Delete removes a row by handle. No-op if absent.
func (b *blobOrphansImpl) Delete(ctx context.Context, handle string) error {
	_, err := b.pool.Exec(ctx,
		`DELETE FROM rimsky_blob_orphans WHERE handle = $1`,
		handle,
	)
	if err != nil {
		return fmt.Errorf("blob_orphans.Delete: %w", err)
	}
	return nil
}
