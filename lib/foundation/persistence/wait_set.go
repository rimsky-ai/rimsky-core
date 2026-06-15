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
	FrameID       shared.UUID
	ReceiverRunID shared.UUID
	SenderRunID   shared.UUID
	// @constraint: TopicKind is one of "terminal" | "transient" | "attribute" | "event" | "message"
	// (the 5-value taxonomy), with "state" tolerated as a legacy/fallback value.
	TopicKind string
	// @constraint: SubscriptionScope is one of "direct" | "instance".
	SubscriptionScope string
	// @constraint: TopicFilter is nullable; carried for observability.
	TopicFilter json.RawMessage
	// @constraint: DrainedAt nil means not yet drained.
	DrainedAt *time.Time
}

// WaitSetTable is the persistence-layer access surface for
// rimsky_wait_set, the per-frame ledger that drives dispatch
// eligibility under the subscription-cascade model.
//
//	@concept: wait-set
type WaitSetTable interface {
	// @agent-contract: Insert adds one wait-set row. Idempotent under the table's PK
	// (frame_id, receiver, sender, topic_kind, subscription_scope) — duplicate
	// inserts within the same transaction are dropped via ON CONFLICT DO
	// NOTHING. Does NOT promote drained rows.
	Insert(ctx context.Context, row WaitSetRow, tx Tx) error

	// @agent-contract: MarkDrainedBySender bulk-marks every wait-set row where
	// (frame_id, sender_run_id) match as drained (sets drained_at to NOW()).
	// Drained rows remain queryable for the substitution-context builder.
	// Idempotent: rows already drained are not re-touched. Does NOT delete
	// rows (replaces the pre-2026-05-20 DeleteBySender semantic).
	MarkDrainedBySender(ctx context.Context, frameID, senderRunID shared.UUID, tx Tx) error

	// @agent-contract: ListForReceiver returns the wait-set rows currently gating the
	// receiver run, used by /admin/diagnostics/wait-sets for stuck-frame
	// debugging. Does NOT filter drained rows.
	ListForReceiver(ctx context.Context, frameID, receiverRunID shared.UUID, tx Tx) ([]WaitSetRow, error)

	// @agent-contract: ListForFrame returns every wait-set row in a frame, used by
	// /admin/diagnostics/wait-sets without a receiver filter. Does NOT
	// filter drained rows.
	ListForFrame(ctx context.Context, frameID shared.UUID, tx Tx) ([]WaitSetRow, error)

	// @agent-contract: ListDrainedAttributeRowsForReceiver returns the drained wait-set
	// rows for the receiver in the frame, filtered to topic_kind='attribute'.
	// Drives the substitution-context builder by enumerating sender_run_ids
	// that contributed to this dispatch via attribute-topic edges. Does NOT
	// return non-drained or non-attribute rows.
	ListDrainedAttributeRowsForReceiver(
		ctx context.Context, frameID, receiverRunID shared.UUID, tx Tx,
	) ([]WaitSetRow, error)
}
