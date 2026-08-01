// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
	Type            *string
	Sender          string
	SenderKind      *string
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
	Insert(ctx context.Context, req EnqueueMessageRequest, tx Tx) error

	MarkDelivered(ctx context.Context, id shared.UUID, frame shared.UUID, deliveredAt time.Time, tx Tx) (bool, error)

	ListPendingForInstance(ctx context.Context, instanceID shared.UUID, tx Tx) ([]MessageRow, error)

	ListDeliveredForFrame(ctx context.Context, frame shared.UUID, tx Tx) ([]MessageRow, error)

	Get(ctx context.Context, id shared.UUID, tx Tx) (*MessageRow, error)

	List(ctx context.Context, filter MessageListFilter, pag ListPagination) (PaginatedListResult[MessageRow], error)

	CancelPendingForInstance(ctx context.Context, instanceID shared.UUID, tx Tx) (int, error)

	PickPendingMessagesForIdleInstances(ctx context.Context, tx Tx) ([]PendingMessagePick, error)
}
