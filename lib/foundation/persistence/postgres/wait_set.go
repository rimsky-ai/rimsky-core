// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: wait-set

package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

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
		   (frame_id, receiver_run_id, sender_run_id, topic_kind, topic_filter)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (frame_id, receiver_run_id, sender_run_id, topic_kind)
		 DO NOTHING`,
		row.FrameID, row.ReceiverNodeRunID, row.SenderNodeRunID,
		row.TopicKind, filter)
	if err != nil {
		return fmt.Errorf("rimsky_wait_set insert: %w", err)
	}
	return nil
}

func (b *waitSetImpl) MarkDrainedBySender(ctx context.Context, frameID, senderNodeRunID shared.UUID, tx persistence.Tx) error {
	ex := b.q(tx)
	_, err := ex.Exec(ctx,
		`UPDATE rimsky_wait_set
		    SET drained_at = NOW()
		  WHERE frame_id = $1 AND sender_run_id = $2 AND drained_at IS NULL`,
		frameID, senderNodeRunID)
	if err != nil {
		return fmt.Errorf("rimsky_wait_set mark drained by sender: %w", err)
	}
	return nil
}

func (b *waitSetImpl) ListForReceiver(ctx context.Context, frameID, receiverNodeRunID shared.UUID, tx persistence.Tx) ([]persistence.WaitSetRow, error) {
	ex := b.q(tx)
	rows, err := ex.Query(ctx,
		`SELECT frame_id, receiver_run_id, sender_run_id, topic_kind, topic_filter, drained_at
		   FROM rimsky_wait_set
		  WHERE frame_id = $1 AND receiver_run_id = $2`,
		frameID, receiverNodeRunID)
	if err != nil {
		return nil, fmt.Errorf("rimsky_wait_set list for receiver: %w", err)
	}
	defer rows.Close()
	return collectWaitSet(rows)
}

func (b *waitSetImpl) ListForFrame(ctx context.Context, frameID shared.UUID, tx persistence.Tx) ([]persistence.WaitSetRow, error) {
	ex := b.q(tx)
	rows, err := ex.Query(ctx,
		`SELECT frame_id, receiver_run_id, sender_run_id, topic_kind, topic_filter, drained_at
		   FROM rimsky_wait_set
		  WHERE frame_id = $1`,
		frameID)
	if err != nil {
		return nil, fmt.Errorf("rimsky_wait_set list for frame: %w", err)
	}
	defer rows.Close()
	return collectWaitSet(rows)
}

// @concept: cascade
// @decision: walker-rule-per-sender-node
func (b *waitSetImpl) ListSenderNodesForReceiver(
	ctx context.Context, frameID, receiverNodeRunID shared.UUID, tx persistence.Tx,
) ([]shared.UUID, error) {
	rows, err := b.q(tx).Query(ctx,
		`SELECT DISTINCT r.node_id
		   FROM rimsky_wait_set w
		   JOIN rimsky_node_runs r ON r.id = w.sender_run_id
		  WHERE w.frame_id = $1 AND w.receiver_run_id = $2`,
		frameID, receiverNodeRunID,
	)
	if err != nil {
		return nil, fmt.Errorf("ListSenderNodesForReceiver: %w", err)
	}
	defer rows.Close()
	var out []shared.UUID
	for rows.Next() {
		var id shared.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("ListSenderNodesForReceiver: scan: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// @concept: cascade
// @decision: walker-rule-per-sender-node
func (b *waitSetImpl) HasRowForSenderRun(
	ctx context.Context, frameID, receiverNodeRunID, senderNodeRunID shared.UUID, tx persistence.Tx,
) (bool, error) {
	var present bool
	err := b.q(tx).QueryRow(ctx,
		`SELECT EXISTS (
		   SELECT 1 FROM rimsky_wait_set
		    WHERE frame_id = $1 AND receiver_run_id = $2 AND sender_run_id = $3
		 )`,
		frameID, receiverNodeRunID, senderNodeRunID,
	).Scan(&present)
	if err != nil {
		return false, fmt.Errorf("HasRowForSenderRun: %w", err)
	}
	return present, nil
}

// @concept: cascade
func (b *waitSetImpl) ListPendingReceiversForDrainedSender(
	ctx context.Context, frameID, senderNodeRunID shared.UUID, tx persistence.Tx,
) ([]shared.UUID, error) {
	rows, err := b.q(tx).Query(ctx,
		`SELECT DISTINCT w.receiver_run_id
		   FROM rimsky_wait_set w
		   JOIN rimsky_node_runs r ON r.id = w.receiver_run_id
		  WHERE w.frame_id = $1 AND w.sender_run_id = $2
		    AND r.state = 'pending'
		    AND r.creation_reason = 'cascade'`,
		frameID, senderNodeRunID,
	)
	if err != nil {
		return nil, fmt.Errorf("ListPendingReceiversForDrainedSender: %w", err)
	}
	defer rows.Close()
	var out []shared.UUID
	for rows.Next() {
		var id shared.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("ListPendingReceiversForDrainedSender: scan: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// @concept: cascade
func (b *waitSetImpl) HasUndrainedRowsForReceiver(
	ctx context.Context, frameID, receiverNodeRunID shared.UUID, tx persistence.Tx,
) (bool, error) {
	var exists bool
	err := b.q(tx).QueryRow(ctx,
		`SELECT EXISTS (
		   SELECT 1 FROM rimsky_wait_set
		    WHERE frame_id = $1 AND receiver_run_id = $2 AND drained_at IS NULL
		 )`, frameID, receiverNodeRunID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("HasUndrainedRowsForReceiver: %w", err)
	}
	return exists, nil
}

func collectWaitSet(rows pgx.Rows) ([]persistence.WaitSetRow, error) {
	out := []persistence.WaitSetRow{}
	for rows.Next() {
		var w persistence.WaitSetRow
		var filter []byte
		var drainedAt *time.Time
		if err := rows.Scan(&w.FrameID, &w.ReceiverNodeRunID, &w.SenderNodeRunID,
			&w.TopicKind, &filter, &drainedAt); err != nil {
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
