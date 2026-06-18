// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type OrphanBlobsArgs struct {
	Persist     persistence.Tables
	BlobOrphans persistence.BlobOrphanTable
	Backend     persistence.BlobBackend
	Clock       shared.Clock
	Logger      shared.Logger
	Limit int
}

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

func reapOneBlobOrphan(ctx context.Context, args OrphanBlobsArgs, row persistence.BlobOrphanRow, log shared.Logger) error {
	if err := args.Backend.Delete(ctx, persistence.Handle(row.Handle)); err != nil {
		if !errors.Is(err, persistence.ErrBlobNotFound) {
			return fmt.Errorf("backend.Delete: %w", err)
		}
	}
	if err := args.BlobOrphans.Delete(ctx, row.Handle); err != nil {
		return fmt.Errorf("blob_orphans.Delete: %w", err)
	}
	log.Debug("tick: reaped blob orphan",
		"handle", row.Handle,
		"backend", row.Backend)
	return nil
}
