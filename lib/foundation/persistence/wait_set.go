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

// @concept: wait-set
type WaitSetRow struct {
	FrameID           shared.UUID
	ReceiverNodeRunID shared.UUID
	SenderNodeRunID   shared.UUID
	TopicKind         string
	TopicFilter       json.RawMessage
	DrainedAt         *time.Time
}

// @concept: wait-set
type WaitSetTable interface {
	Insert(ctx context.Context, row WaitSetRow, tx Tx) error

	MarkDrainedBySender(ctx context.Context, frameID, senderNodeRunID shared.UUID, tx Tx) error

	ListForReceiver(ctx context.Context, frameID, receiverNodeRunID shared.UUID, tx Tx) ([]WaitSetRow, error)

	ListForFrame(ctx context.Context, frameID shared.UUID, tx Tx) ([]WaitSetRow, error)

	ListDrainedAttributeRowsForReceiver(
		ctx context.Context, frameID, receiverNodeRunID shared.UUID, tx Tx,
	) ([]WaitSetRow, error)

	// @concept: cascade
	// @decision: walker-rule-per-sender-node
	ListSenderNodesForReceiver(ctx context.Context, frameID, receiverNodeRunID shared.UUID, tx Tx) ([]shared.UUID, error)

	// @concept: cascade
	// @decision: walker-rule-per-sender-node
	HasRowForSenderRun(ctx context.Context, frameID, receiverNodeRunID, senderNodeRunID shared.UUID, tx Tx) (bool, error)

	// @concept: cascade
	ListPendingReceiversForDrainedSender(ctx context.Context, frameID, senderNodeRunID shared.UUID, tx Tx) ([]shared.UUID, error)

	// @concept: cascade
	HasUndrainedRowsForReceiver(ctx context.Context, frameID, receiverNodeRunID shared.UUID, tx Tx) (bool, error)
}
