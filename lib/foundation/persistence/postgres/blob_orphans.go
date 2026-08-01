// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func (s *tablesImpl) BlobOrphans() persistence.BlobOrphanTable {
	return (*blobOrphansImpl)(s)
}

type blobOrphansImpl tablesImpl

func (b *blobOrphansImpl) q(tx persistence.Tx) querier { return (*tablesImpl)(b).q(tx) }

var _ persistence.BlobOrphanTable = (*blobOrphansImpl)(nil)

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

func (b *blobOrphansImpl) DueBefore(ctx context.Context, cutoff time.Time, backend string, limit int) ([]persistence.BlobOrphanRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := b.pool.Query(ctx,
		`SELECT handle, backend, orphaned_at, reap_after
		   FROM rimsky_blob_orphans
		  WHERE reap_after <= $1 AND backend = $2
		  ORDER BY reap_after ASC
		  LIMIT $3`,
		cutoff, backend, limit,
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
