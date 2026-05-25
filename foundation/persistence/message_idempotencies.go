// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// message_idempotencies.go — universal idempotency dedup-tuple table
// interface for POST /instances/{id}/messages.
//
// The control-api handler computes a dedup key `(instance_id, sender,
// idempotency_key)` from the Idempotency-Key HTTP header. InsertOrLookup
// atomically inserts the tuple if absent and returns the new message id;
// on conflict it returns the previously-recorded message id without
// inserting a duplicate envelope. Operators and publishers can retry
// safely under at-most-once semantics.
//
// @concept: message
package persistence

import (
	"context"
	"time"

	"github.com/fallguyconsulting/rimsky/foundation/shared"
)

// MessageIdempotencyRow is the persisted dedup tuple.
type MessageIdempotencyRow struct {
	InstanceID     shared.UUID
	Sender         string
	IdempotencyKey string
	MessageID      shared.UUID
	CreatedAt      time.Time
}

// MessageIdempotencyTable is the dedup-tuple persistence interface. The
// surface is intentionally narrow — one atomic INSERT-or-lookup write
// path used inside the message-create tx, plus a retention sweep.
type MessageIdempotencyTable interface {
	// InsertOrLookup attempts INSERT of the tuple. On unique-key conflict
	// it returns the previously-recorded MessageID with inserted=false.
	// On fresh insert it returns the supplied row with inserted=true.
	// Runs inside the caller's tx so the idempotency row and the message
	// envelope insert atomically together.
	InsertOrLookup(ctx context.Context, tx Tx, row MessageIdempotencyRow) (MessageIdempotencyRow, bool, error)

	// DeleteOlderThan removes rows with created_at < cutoff. Returns the
	// count of deleted rows. Called by the scheduler-tick retention sweep
	// under the advisory lock.
	DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}
