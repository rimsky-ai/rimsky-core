// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: message

package persistence

import (
	"context"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// MessageIdempotencyRow is the persisted dedup tuple.
type MessageIdempotencyRow struct {
	InstanceID shared.UUID
	// SenderKind is the structural source-of-claim discriminator:
	// "operator" / "publisher" / "anonymous". Namespaces `Sender` so
	// a publisher named "operator" cannot collide with operator-side
	// emits. See package doc.
	SenderKind string
	Sender     string
	// SenderSubject discriminates operator-side requests by api-key so
	// two distinct api-keys posting to the same instance with the same
	// Idempotency-Key can no longer cross-collide. See package doc.
	SenderSubject  string
	IdempotencyKey string
	MessageID      shared.UUID
	CreatedAt      time.Time
}

// MessageIdempotencyTable is the dedup-tuple persistence interface. The
// surface is intentionally narrow — one atomic INSERT-or-lookup write
// path used inside the message-create tx, plus a retention sweep.
type MessageIdempotencyTable interface {
	// @agent-contract: InsertOrLookup attempts INSERT of the tuple. On
	// unique-key conflict it returns the previously-recorded MessageID with
	// inserted=false. On fresh insert it returns the supplied row with
	// inserted=true. Runs inside the caller's tx so the idempotency row and
	// the message envelope insert atomically together. Does NOT handle tx
	// lifecycle — the caller owns commit/rollback.
	InsertOrLookup(ctx context.Context, tx Tx, row MessageIdempotencyRow) (MessageIdempotencyRow, bool, error)

	// @agent-contract: DeleteOlderThan removes rows with created_at < cutoff
	// and returns the count of deleted rows. Called by the scheduler-tick
	// retention sweep under the advisory lock. Does NOT acquire the advisory
	// lock itself — the caller is responsible.
	DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}
