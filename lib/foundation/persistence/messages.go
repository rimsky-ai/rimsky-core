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

type MessageRow struct {
	ID          shared.UUID
	InstanceID  shared.UUID
	Type        string
	Sender      string
	SenderKind  string
	Payload     json.RawMessage
	ReceivedAt  time.Time
	DeliveredAt *time.Time
	FrameID     *shared.UUID
	Cancelled   bool
}

// @decision: empty-message-as-root-trigger
func (m MessageRow) IsEmptyWake() bool { return m.Type == "" }

type EnqueueMessageRequest struct {
	ID         shared.UUID
	InstanceID shared.UUID
	Type       string
	Sender     string
	SenderKind string
	Payload    json.RawMessage
	ReceivedAt time.Time
}

type MessageListFilter struct {
	InstanceID      *shared.UUID
	Type            string
	Sender          string
	SenderKind      string
	FrameID         *shared.UUID
	DeliveredAfter  *time.Time
	DeliveredBefore *time.Time
	Pending         *bool
}

type PendingMessagePick struct {
	InstanceID shared.UUID
	MessageID  shared.UUID
}

type MessageTable interface {
	Insert(ctx context.Context, tx Tx, req EnqueueMessageRequest) error

	MarkDelivered(ctx context.Context, tx Tx, id shared.UUID, frame shared.UUID, deliveredAt time.Time) (bool, error)

	ListPendingForInstance(ctx context.Context, tx Tx, instanceID shared.UUID) ([]MessageRow, error)

	ListDeliveredForFrame(ctx context.Context, tx Tx, frame shared.UUID) ([]MessageRow, error)

	Get(ctx context.Context, id shared.UUID) (*MessageRow, error)

	GetInTx(ctx context.Context, tx Tx, id shared.UUID) (*MessageRow, error)

	List(ctx context.Context, filter MessageListFilter, pag ListPagination) (PaginatedListResult[MessageRow], error)

	CancelPendingForInstance(ctx context.Context, tx Tx, instanceID shared.UUID) (int, error)

	PickPendingMessagesForIdleInstances(ctx context.Context, tx Tx) ([]PendingMessagePick, error)
}
