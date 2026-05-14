// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package persistence

import (
	"context"
	"encoding/json"

	"github.com/fallguy/rimsky/foundation/shared"
)

// WaitSetRow is one row of rimsky_wait_set.
//
//	@concept: wait-set
type WaitSetRow struct {
	FrameID           shared.UUID
	ReceiverNodeID    shared.UUID
	SenderNodeID      shared.UUID
	TopicKind         string          // "state" | "attribute" | "event"
	SubscriptionScope string          // "direct" | "instance"
	TopicFilter       json.RawMessage // nullable; carried for observability
}

// WaitSetTable is the persistence-layer access surface for
// rimsky_wait_set, the per-frame ledger that drives dispatch
// eligibility under the subscription-cascade model.
//
//	@concept: wait-set
type WaitSetTable interface {
	// Insert adds one wait-set row. Idempotent under the table's PK
	// (frame_id, receiver, sender, topic_kind, subscription_scope) —
	// duplicate inserts within the same transaction are dropped via
	// ON CONFLICT DO NOTHING.
	Insert(ctx context.Context, row WaitSetRow, tx Tx) error

	// DeleteBySender bulk-deletes every wait-set row where
	// (frame_id, sender_node_id) match. Drains the sender from every
	// receiver's wait-set in one statement. Called by the cascade walk
	// when a sender reaches any settled state (fresh / failed / parked).
	DeleteBySender(ctx context.Context, frameID, senderID shared.UUID, tx Tx) error

	// ListForReceiver returns the wait-set rows currently gating the
	// receiver. Used by /admin/diagnostics/wait-sets for stuck-frame
	// debugging.
	ListForReceiver(ctx context.Context, frameID, receiverID shared.UUID, tx Tx) ([]WaitSetRow, error)

	// ListForFrame returns every wait-set row in a frame. Used by
	// /admin/diagnostics/wait-sets without a receiver filter.
	ListForFrame(ctx context.Context, frameID shared.UUID, tx Tx) ([]WaitSetRow, error)
}
