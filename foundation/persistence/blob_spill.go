// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"time"
)

// ShouldSpillBlob reports whether a payload of size N bytes should be
// written through the configured BlobBackend instead of stored inline.
// Returns false when:
//   - the backend is nil (spill disabled by operator),
//   - the threshold is ≤ 0 (spill disabled),
//   - the backend's Name() == "inline" (degenerate),
//   - the size is ≤ threshold.
//
// The check is intentionally identical to
// foundation/integration/runner_terminal_park.go::shouldSpillBlob; both
// sites must agree so a value spilled at write time can be read back
// without ambiguity. Per plan §D6.
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

// DefaultOrphanRetention is the fallback retention window used when the
// driver's BlobRetention() returns 0. Matches BlobRetentionConfig's
// documented default.
const DefaultOrphanRetention = 24 * time.Hour

// QueueBlobOrphan inserts (handle, backend) into rimsky_blob_orphans
// with reap_after = now + retention. Idempotent on handle PK conflict
// (an already-queued handle is a no-op). Used by the attribute and
// parked-payload write paths when overwriting a row that previously
// held a spilled handle. Per plan §D6 step 4.
//
// Empty handle is a no-op (caller had nothing to orphan). When retention
// is ≤ 0, falls back to DefaultOrphanRetention.
func QueueBlobOrphan(
	ctx context.Context,
	orphans BlobOrphansStore,
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
