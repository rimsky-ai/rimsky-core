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
// Post-stage-5 of the run-row lifecycle cutover the receiver / sender
// columns lift to per-run identity (`rimsky_node_runs.id`) — the
// wait-set ledger that gates dispatch under the subscription-cascade
// model now binds to specific runs so two in-flight runs of the same
// node-type (in different frames, or under future sub-graph invocations)
// don't conflate their wait-sets.
//
//	@concept: wait-set
type WaitSetRow struct {
	FrameID           shared.UUID
	ReceiverRunID     shared.UUID
	SenderRunID       shared.UUID
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
	// (frame_id, sender_run_id) match. Drains the sender from every
	// receiver's wait-set in one statement. Called by the cascade walk
	// when a sender reaches any settled state (fresh / failed / parked).
	DeleteBySender(ctx context.Context, frameID, senderRunID shared.UUID, tx Tx) error

	// ListForReceiver returns the wait-set rows currently gating the
	// receiver run. Used by /admin/diagnostics/wait-sets for stuck-frame
	// debugging.
	ListForReceiver(ctx context.Context, frameID, receiverRunID shared.UUID, tx Tx) ([]WaitSetRow, error)

	// ListForFrame returns every wait-set row in a frame. Used by
	// /admin/diagnostics/wait-sets without a receiver filter.
	ListForFrame(ctx context.Context, frameID shared.UUID, tx Tx) ([]WaitSetRow, error)
}
