// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Postgres impl of persistence.WaitSetTable — the per-frame ledger that
// drives dispatch eligibility under the subscription-cascade model.
//
//	@concept: wait-set
package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
)

// WaitSet returns the postgres WaitSetTable impl.
func (s *tablesImpl) WaitSet() persistence.WaitSetTable {
	return (*waitSetImpl)(s)
}

type waitSetImpl tablesImpl

func (b *waitSetImpl) q(tx persistence.Tx) querier { return (*tablesImpl)(b).q(tx) }

var _ persistence.WaitSetTable = (*waitSetImpl)(nil)

func (b *waitSetImpl) Insert(ctx context.Context, row persistence.WaitSetRow, tx persistence.Tx) error {
	ex := b.q(tx)
	var filter any
	if len(row.TopicFilter) > 0 {
		filter = []byte(row.TopicFilter)
	}
	_, err := ex.Exec(ctx,
		`INSERT INTO rimsky_wait_set
		   (frame_id, receiver_run_id, sender_run_id, topic_kind, subscription_scope, topic_filter)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (frame_id, receiver_run_id, sender_run_id, topic_kind, subscription_scope)
		 DO NOTHING`,
		row.FrameID, row.ReceiverRunID, row.SenderRunID,
		row.TopicKind, row.SubscriptionScope, filter)
	if err != nil {
		return fmt.Errorf("rimsky_wait_set insert: %w", err)
	}
	return nil
}

// MarkDrainedBySender marks rows drained rather than deleting them.
// Idempotent: the `AND drained_at IS NULL` guard means re-invoking
// against an already-drained set is a no-op.
func (b *waitSetImpl) MarkDrainedBySender(ctx context.Context, frameID, senderRunID shared.UUID, tx persistence.Tx) error {
	ex := b.q(tx)
	_, err := ex.Exec(ctx,
		`UPDATE rimsky_wait_set
		    SET drained_at = NOW()
		  WHERE frame_id = $1 AND sender_run_id = $2 AND drained_at IS NULL`,
		frameID, senderRunID)
	if err != nil {
		return fmt.Errorf("rimsky_wait_set mark drained by sender: %w", err)
	}
	return nil
}

func (b *waitSetImpl) ListForReceiver(ctx context.Context, frameID, receiverRunID shared.UUID, tx persistence.Tx) ([]persistence.WaitSetRow, error) {
	ex := b.q(tx)
	rows, err := ex.Query(ctx,
		`SELECT frame_id, receiver_run_id, sender_run_id, topic_kind, subscription_scope, topic_filter, drained_at
		   FROM rimsky_wait_set
		  WHERE frame_id = $1 AND receiver_run_id = $2`,
		frameID, receiverRunID)
	if err != nil {
		return nil, fmt.Errorf("rimsky_wait_set list for receiver: %w", err)
	}
	defer rows.Close()
	return collectWaitSet(rows)
}

func (b *waitSetImpl) ListForFrame(ctx context.Context, frameID shared.UUID, tx persistence.Tx) ([]persistence.WaitSetRow, error) {
	ex := b.q(tx)
	rows, err := ex.Query(ctx,
		`SELECT frame_id, receiver_run_id, sender_run_id, topic_kind, subscription_scope, topic_filter, drained_at
		   FROM rimsky_wait_set
		  WHERE frame_id = $1`,
		frameID)
	if err != nil {
		return nil, fmt.Errorf("rimsky_wait_set list for frame: %w", err)
	}
	defer rows.Close()
	return collectWaitSet(rows)
}

// ListDrainedAttributeRowsForReceiver returns drained attribute-topic
// rows for the receiver — the contributing set for the substitution-
// context builder at dispatch time.
func (b *waitSetImpl) ListDrainedAttributeRowsForReceiver(
	ctx context.Context, frameID, receiverRunID shared.UUID, tx persistence.Tx,
) ([]persistence.WaitSetRow, error) {
	ex := b.q(tx)
	rows, err := ex.Query(ctx,
		`SELECT frame_id, receiver_run_id, sender_run_id, topic_kind, subscription_scope, topic_filter, drained_at
		   FROM rimsky_wait_set
		  WHERE frame_id = $1 AND receiver_run_id = $2
		    AND drained_at IS NOT NULL
		    AND topic_kind = 'attribute'
		  ORDER BY drained_at ASC, sender_run_id ASC`,
		frameID, receiverRunID)
	if err != nil {
		return nil, fmt.Errorf("rimsky_wait_set list drained attribute rows: %w", err)
	}
	defer rows.Close()
	return collectWaitSet(rows)
}

func collectWaitSet(rows pgx.Rows) ([]persistence.WaitSetRow, error) {
	out := []persistence.WaitSetRow{}
	for rows.Next() {
		var w persistence.WaitSetRow
		var filter []byte
		var drainedAt *time.Time
		if err := rows.Scan(&w.FrameID, &w.ReceiverRunID, &w.SenderRunID,
			&w.TopicKind, &w.SubscriptionScope, &filter, &drainedAt); err != nil {
			return nil, err
		}
		if filter != nil {
			w.TopicFilter = json.RawMessage(filter)
		}
		w.DrainedAt = drainedAt
		out = append(out, w)
	}
	return out, rows.Err()
}
