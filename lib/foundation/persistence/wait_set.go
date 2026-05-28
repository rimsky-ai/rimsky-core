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

// WaitSetRow is one row of rimsky_wait_set.
//
// Post-stage-5 of the run-row lifecycle cutover the receiver / sender
// columns lift to per-run identity (`rimsky_node_runs.id`) — the
// wait-set ledger that gates dispatch under the subscription-cascade
// model now binds to specific runs so two in-flight runs of the same
// node-type (in different frames, or under future sub-graph invocations)
// don't conflate their wait-sets.
//
// Under per-run attribute keying (2026-05-20), drain marks the row's
// DrainedAt timestamp instead of deleting the row. Drained rows remain
// queryable by the substitution-context builder.
//
//	@concept: wait-set
type WaitSetRow struct {
	FrameID           shared.UUID
	ReceiverRunID     shared.UUID
	SenderRunID       shared.UUID
	TopicKind         string          // "state" | "attribute" | "event"
	SubscriptionScope string          // "direct" | "instance"
	TopicFilter       json.RawMessage // nullable; carried for observability
	DrainedAt         *time.Time      // nil means not yet drained
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

	// MarkDrainedBySender bulk-marks every wait-set row where
	// (frame_id, sender_run_id) match as drained (sets drained_at to NOW()).
	// Drained rows remain queryable for the substitution-context builder.
	// Idempotent: rows already drained are not re-touched. Replaces the
	// prior DeleteBySender semantic (rows used to be deleted on drain;
	// post-2026-05-20 they're retained for trigger-context queries).
	MarkDrainedBySender(ctx context.Context, frameID, senderRunID shared.UUID, tx Tx) error

	// ListForReceiver returns the wait-set rows currently gating the
	// receiver run. Used by /admin/diagnostics/wait-sets for stuck-frame
	// debugging.
	ListForReceiver(ctx context.Context, frameID, receiverRunID shared.UUID, tx Tx) ([]WaitSetRow, error)

	// ListForFrame returns every wait-set row in a frame. Used by
	// /admin/diagnostics/wait-sets without a receiver filter.
	ListForFrame(ctx context.Context, frameID shared.UUID, tx Tx) ([]WaitSetRow, error)

	// ListDrainedAttributeRowsForReceiver returns the drained wait-set
	// rows for the receiver in the frame, filtered to topic_kind='attribute'.
	// Used by the substitution-context builder to enumerate sender_run_ids
	// that contributed to this dispatch via attribute-topic edges.
	//
	// Per .ok-planner/specs/2026-05-20-attribute-pull-resolution-design.md
	// §"Substitution context builder".
	ListDrainedAttributeRowsForReceiver(
		ctx context.Context, frameID, receiverRunID shared.UUID, tx Tx,
	) ([]WaitSetRow, error)
}
