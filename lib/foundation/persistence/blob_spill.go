// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"time"
)

func ShouldSpillBlob(bb BlobBackend, threshold int, size int) bool {
	if size <= 0 {
		return false
	}
	if bb == nil {
		return false
	}
	if threshold <= 0 {
		return false
	}
	if bb.Name() == "inline" {
		return false
	}
	return size > threshold
}

const DefaultOrphanRetention = 24 * time.Hour

func QueueBlobOrphan(
	ctx context.Context,
	orphans BlobOrphanTable,
	tx Tx,
	handle, backend string,
	now time.Time,
	retention time.Duration,
) error {
	if handle == "" || orphans == nil {
		return nil
	}
	if retention <= 0 {
		retention = DefaultOrphanRetention
	}
	return orphans.Insert(ctx, BlobOrphanRow{
		Handle:     handle,
		Backend:    backend,
		OrphanedAt: now,
		ReapAfter:  now.Add(retention),
	}, tx)
}
