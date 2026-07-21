// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package sqlite

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
	_, err := b.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_blob_orphans (handle, backend, orphaned_at, reap_after)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(handle) DO NOTHING`,
		row.Handle, row.Backend, formatTime(row.OrphanedAt), formatTime(row.ReapAfter),
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
	rows, err := b.db.QueryContext(ctx,
		`SELECT handle, backend, orphaned_at, reap_after
		   FROM rimsky_blob_orphans
		  WHERE reap_after <= ? AND backend = ?
		  ORDER BY reap_after ASC
		  LIMIT ?`,
		formatTime(cutoff), backend, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("blob_orphans.DueBefore: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []persistence.BlobOrphanRow
	for rows.Next() {
		var (
			r          persistence.BlobOrphanRow
			orphanedAt string
			reapAfter  string
		)
		if err := rows.Scan(&r.Handle, &r.Backend, &orphanedAt, &reapAfter); err != nil {
			return nil, fmt.Errorf("blob_orphans.DueBefore: scan: %w", err)
		}
		ot, err := parseTime(orphanedAt)
		if err != nil {
			return nil, fmt.Errorf("blob_orphans.DueBefore: orphaned_at: %w", err)
		}
		rt, err := parseTime(reapAfter)
		if err != nil {
			return nil, fmt.Errorf("blob_orphans.DueBefore: reap_after: %w", err)
		}
		r.OrphanedAt = ot
		r.ReapAfter = rt
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("blob_orphans.DueBefore: rows.Err: %w", err)
	}
	return out, nil
}

func (b *blobOrphansImpl) Delete(ctx context.Context, handle string) error {
	_, err := b.db.ExecContext(ctx,
		`DELETE FROM rimsky_blob_orphans WHERE handle = ?`,
		handle,
	)
	if err != nil {
		return fmt.Errorf("blob_orphans.Delete: %w", err)
	}
	return nil
}
