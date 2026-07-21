// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"time"
)

type BlobOrphanRow struct {
	Handle     string
	Backend    string
	OrphanedAt time.Time
	ReapAfter  time.Time
}

type BlobOrphanTable interface {
	Insert(ctx context.Context, row BlobOrphanRow, tx Tx) error
	DueBefore(ctx context.Context, cutoff time.Time, backend string, limit int) ([]BlobOrphanRow, error)
	Delete(ctx context.Context, handle string) error
}
