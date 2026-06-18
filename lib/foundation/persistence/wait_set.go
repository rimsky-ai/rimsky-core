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

//	@concept: wait-set
type WaitSetRow struct {
	FrameID       shared.UUID
	ReceiverRunID shared.UUID
	SenderRunID   shared.UUID
	TopicKind string
	SubscriptionScope string
	TopicFilter json.RawMessage
	DrainedAt *time.Time
}

//	@concept: wait-set
type WaitSetTable interface {
	Insert(ctx context.Context, row WaitSetRow, tx Tx) error

	MarkDrainedBySender(ctx context.Context, frameID, senderRunID shared.UUID, tx Tx) error

	ListForReceiver(ctx context.Context, frameID, receiverRunID shared.UUID, tx Tx) ([]WaitSetRow, error)

	ListForFrame(ctx context.Context, frameID shared.UUID, tx Tx) ([]WaitSetRow, error)

	ListDrainedAttributeRowsForReceiver(
		ctx context.Context, frameID, receiverRunID shared.UUID, tx Tx,
	) ([]WaitSetRow, error)
}
