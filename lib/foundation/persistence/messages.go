// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package persistence

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// MessageRow is the per-row representation of table:rimsky_messages.
//
// Envelopes are inert in rimsky per @blessed-invariant 21: payload
// bytes are read by named-field path only at substitution-leaf
// extraction (graph/attribute/substitution.go::walkPath); never
// logged, formatted, or otherwise inspected.
//
// SenderKind is one of {"operator", "publisher", "instance"} per spec
// .ok-planner/specs/2026-05-17-sensor-messaging-unification-design.md
// §Publisher protocol unification.
type MessageRow struct {
	ID                  shared.UUID
	InstanceID          shared.UUID
	Kind                string // "invalidate" in V1
	Sender              string
	SenderKind          string
	Target              string          // node alias; "" when broadcast
	Payload             json.RawMessage // opaque per @blessed-invariant 21
	BackfillOperationID *shared.UUID    // non-nil when part of a backfill
	ReceivedAt          time.Time
	DeliveredAt         *time.Time
	FrameID             *shared.UUID
	Cancelled           bool
}

// EnqueueMessageRequest is the payload for MessagesTable.Insert.
type EnqueueMessageRequest struct {
	ID                  shared.UUID
	InstanceID          shared.UUID
	Kind                string
	Sender              string
	SenderKind          string
	Target              string
	Payload             json.RawMessage
	BackfillOperationID *shared.UUID
	ReceivedAt          time.Time
}

// MessageListFilter selects rows for the messages list endpoints.
type MessageListFilter struct {
	InstanceID          *shared.UUID
	Kind                string
	SenderKind          string
	Target              string
	BackfillOperationID *shared.UUID
	DeliveredAfter      *time.Time
	DeliveredBefore     *time.Time
}

// MessagesTable is the per-row-type Table accessor for
// table:rimsky_messages. Used by:
//
//   - runtime/message_delivery.go — EnqueueMessage / DeliverPendingMessages
//   - control/controlapi/messages.go — list / detail endpoints
//   - runtime/backfill.go — CreateBackfill / CancelBackfill
type MessagesTable interface {
	// Insert enqueues a new message row (delivered_at IS NULL).
	Insert(ctx context.Context, tx Tx, req EnqueueMessageRequest) error

	// MarkDelivered sets delivered_at = now() and frame_id = frame for
	// the given message id. Returns delivered=true when exactly one
	// row was updated.
	MarkDelivered(ctx context.Context, tx Tx, id shared.UUID, frame shared.UUID, deliveredAt time.Time) (bool, error)

	// MarkCancelled sets cancelled=TRUE (and delivered_at=now() with
	// frame_id NULL) on the message rows matching backfill_operation_id
	// that have not yet been delivered. Used by CancelBackfill.
	MarkCancelled(ctx context.Context, tx Tx, backfillOperationID shared.UUID, at time.Time) (int, error)

	// ListPendingForInstance returns pending messages for an instance,
	// ordered by received_at ascending. Used at frame-boundary
	// delivery.
	ListPendingForInstance(ctx context.Context, tx Tx, instanceID shared.UUID) ([]MessageRow, error)

	// Get returns a single message by id, or nil when absent.
	Get(ctx context.Context, id shared.UUID) (*MessageRow, error)

	// List returns messages matching filter, paginated.
	List(ctx context.Context, filter MessageListFilter, pag ListPagination) (PaginatedListResult[MessageRow], error)
}
