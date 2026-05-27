// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// orphan_blobs.go runs the SweepOrphanedBlobs sweep — D8 of the
// 2026-05-08 platform-extensions plan. Walks rimsky_blob_orphans for
// rows whose reap_after has passed; for each, calls
// BlobBackend.Delete(handle) and removes the row on success.
//
// Errors other than ErrBlobNotFound are logged and the row is left in
// place for retry next tick. ErrBlobNotFound (handle already gone) is
// treated as success — the orphan tracker entry is removed because the
// underlying bytes are no longer present anywhere.
//
// Wired into the conductor tick at a separate cadence
// (BlobConfig.Retention.OrphanSweepInterval, default 1h).

package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/foundation/shared"
)

// OrphanBlobsArgs bundles the dependencies for SweepOrphanedBlobs.
//
// Backend is the active BlobBackend; only handles whose recorded backend
// matches Backend.Name() are reaped (cross-backend handles in a mixed
// deployment are left for that backend's own sweep — v1 only supports a
// single backend, so this is forward-compatible defense).
type OrphanBlobsArgs struct {
	Persist     persistence.Tables
	BlobOrphans persistence.BlobOrphanTable
	Backend     persistence.BlobBackend
	Clock       shared.Clock
	Logger      shared.Logger
	// Limit caps the per-tick reap budget. 0 → 100.
	Limit int
}

// SweepOrphanedBlobs reaps every blob orphan whose reap_after <= now()
// and whose recorded backend matches the active backend.
func SweepOrphanedBlobs(ctx context.Context, args OrphanBlobsArgs) error {
	log := args.Logger
	if log == nil {
		log = shared.SilentLogger{}
	}
	if args.Backend == nil {
		return fmt.Errorf("SweepOrphanedBlobs: nil backend")
	}
	if args.BlobOrphans == nil {
		return fmt.Errorf("SweepOrphanedBlobs: nil blob-orphans store")
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 100
	}
	now := time.Now()
	if args.Clock != nil {
		now = args.Clock.Now()
	}
	rows, err := args.BlobOrphans.DueBefore(ctx, now, limit)
	if err != nil {
		return fmt.Errorf("SweepOrphanedBlobs: list: %w", err)
	}
	for _, r := range rows {
		if r.Backend != args.Backend.Name() {
			// Mismatched-backend orphan: leave for that backend's sweep.
			continue
		}
		if err := reapOneBlobOrphan(ctx, args, r, log); err != nil {
			log.Warn("tick: reap blob orphan failed",
				"handle", r.Handle,
				"backend", r.Backend,
				"error", err.Error())
		}
	}
	return nil
}

// reapOneBlobOrphan deletes the bytes from the backend and removes the
// tracker row. ErrBlobNotFound from Delete is treated as success — bytes
// are gone, tracker should follow.
func reapOneBlobOrphan(ctx context.Context, args OrphanBlobsArgs, row persistence.BlobOrphanRow, log shared.Logger) error {
	if err := args.Backend.Delete(ctx, persistence.Handle(row.Handle)); err != nil {
		if !errors.Is(err, persistence.ErrBlobNotFound) {
			return fmt.Errorf("backend.Delete: %w", err)
		}
		// Treat NotFound as success-and-forget below.
	}
	if err := args.BlobOrphans.Delete(ctx, row.Handle); err != nil {
		return fmt.Errorf("blob_orphans.Delete: %w", err)
	}
	log.Debug("tick: reaped blob orphan",
		"handle", row.Handle,
		"backend", row.Backend)
	return nil
}
