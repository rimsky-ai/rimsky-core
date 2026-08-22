// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: message

package persistence

import (
	"context"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// @decision: message-sender-kind-discriminator
const (
	DedupSenderKindOperator  = "operator"
	DedupSenderKindPublisher = "publisher"
	DedupSenderKindInstance  = "instance"
	DedupSenderKindAnonymous = "anonymous"
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

// @decision: message-sender-kind-discriminator
func (r MessageIdempotencyRow) ValidateSenderKind() error {
	switch r.SenderKind {
	case DedupSenderKindOperator, DedupSenderKindPublisher, DedupSenderKindInstance, DedupSenderKindAnonymous:
		return nil
	}
	return fmt.Errorf("message idempotency: unknown sender_kind %q (want %s|%s|%s|%s)",
		r.SenderKind,
		DedupSenderKindOperator, DedupSenderKindPublisher, DedupSenderKindInstance, DedupSenderKindAnonymous)
}
