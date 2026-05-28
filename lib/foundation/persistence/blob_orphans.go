// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"time"
)

// BlobOrphanRow mirrors a row of rimsky_blob_orphans. The table tracks
// blob handles whose referencing rows have been deleted or overwritten;
// the SweepOrphanedBlobs sweep deletes rows where reap_after <= now()
// and calls BlobBackend.Delete(handle) for each.
type BlobOrphanRow struct {
	Handle     string
	Backend    string
	OrphanedAt time.Time
	ReapAfter  time.Time
}

// BlobOrphanTable is the rimsky_blob_orphans accessor used by:
//   - the attribute write path (D6: queue an orphan when value_handle
//     is overwritten on a row that already had one).
//   - the parked-payload write path (E1: queue an orphan when a
//     parked_payload_handle is replaced or cleared on resume).
//   - the named-event ledger cleanup at instance termination (F5:
//     queue orphans for every payload_handle on a deleted row).
//   - the SweepOrphanedBlobs sweep (D8: list rows whose reap_after has
//     passed; delete after the backend.Delete succeeds).
//
// Insert is idempotent on Handle (PK conflict is treated as already-queued).
// DueBefore returns rows ordered by reap_after ascending; the sweep can
// page through results to bound per-tick work.
//
// Tx-passing convention: Insert takes the caller's tx so the orphan
// queueing is atomic with the row that overwrote the handle. DueBefore
// and Delete are intentionally tx-less — the sweep runs each
// reap-and-delete as an independent unit (a per-row failure should not
// hold a long sweep transaction open) and the cross-step atomicity
// requirement is "delete tracker only after backend.Delete succeeds,"
// which the sweep enforces in the application layer.
type BlobOrphanTable interface {
	Insert(ctx context.Context, row BlobOrphanRow, tx Tx) error
	DueBefore(ctx context.Context, cutoff time.Time, limit int) ([]BlobOrphanRow, error)
	Delete(ctx context.Context, handle string) error
}
