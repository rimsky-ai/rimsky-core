// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// message_idempotencies.go — universal idempotency dedup-tuple table
// interface for POST /instances/{id}/messages.
//
// The control-api handler computes a dedup key `(instance_id,
// sender_kind, sender, sender_subject, idempotency_key)` from the
// Idempotency-Key HTTP header plus the structural sender_kind and the
// requester subject. InsertOrLookup atomically inserts the tuple if
// absent and returns the new message id; on conflict it returns the
// previously-recorded message id without inserting a duplicate
// envelope. Operators and publishers can retry safely under
// at-most-once semantics.
//
// The dedup tuple carries TWO discriminators beyond the obvious
// (instance_id, idempotency_key) pair so two distinct callers cannot
// cross-collide:
//
//   - SenderKind is the structural source-of-claim discriminator:
//     "operator" / "publisher" / "anonymous". This namespaces the bare
//     `sender` string by code path so a publisher whose operator-chosen
//     publisher_name is the literal `"operator"` no longer shares a
//     dedup tuple with operator-side emits — the operator path always
//     writes sender_kind="operator" (or "anonymous"), the publisher
//     path always writes sender_kind="publisher".
//   - SenderSubject is the per-caller discriminator WITHIN
//     sender_kind="operator". Operator-side requests share the
//     hard-coded literal sender="operator", so two distinct api-keys
//     would otherwise collide on the same Idempotency-Key against the
//     same instance — caller B would get caller A's message_id back as
//     a "replay", and caller B could probe whether caller A used a
//     given key by sending it. SenderSubject carries the per-caller
//     identity that breaks the collision:
//   - Operator with api-key   → the api-key UUID as a string.
//   - Operator anonymous-mode → "anonymous".
//   - Publisher              → "" (empty); the `sender` column
//     already carries the per-publisher publisher_name and
//     provides isolation.
//
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
