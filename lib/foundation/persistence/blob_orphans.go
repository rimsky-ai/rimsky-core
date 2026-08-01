// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
