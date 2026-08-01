// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: message

package persistence

import (
	"context"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type MessageIdempotencyRow struct {
	InstanceID     shared.UUID
	SenderKind     string
	Sender         string
	SenderSubject  string
	IdempotencyKey string
	MessageID      shared.UUID
	CreatedAt      time.Time
}

type MessageIdempotencyTable interface {
	InsertOrLookup(ctx context.Context, row MessageIdempotencyRow, tx Tx) (MessageIdempotencyRow, bool, error)

	DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}
