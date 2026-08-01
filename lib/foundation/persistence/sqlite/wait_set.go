// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: wait-set

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

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
	var filter any
	if len(row.TopicFilter) > 0 {
		filter = string(row.TopicFilter)
	}
	_, err := b.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_wait_set
		   (frame_id, receiver_run_id, sender_run_id, topic_kind, topic_filter)
		 VALUES (?, ?, ?, ?, ?)
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
	_, err := b.q(tx).ExecContext(ctx,
		`UPDATE rimsky_wait_set
		    SET drained_at = ?
		  WHERE frame_id = ? AND sender_run_id = ? AND drained_at IS NULL`,
		nowUTC(), frameID, senderNodeRunID)
	if err != nil {
		return fmt.Errorf("rimsky_wait_set mark drained by sender: %w", err)
	}
	return nil
}

func (b *waitSetImpl) ListForReceiver(ctx context.Context, frameID, receiverNodeRunID shared.UUID, tx persistence.Tx) ([]persistence.WaitSetRow, error) {
	rows, err := b.q(tx).QueryContext(ctx,
		`SELECT frame_id, receiver_run_id, sender_run_id, topic_kind, topic_filter, drained_at
		   FROM rimsky_wait_set
		  WHERE frame_id = ? AND receiver_run_id = ?`,
		frameID, receiverNodeRunID)
	if err != nil {
		return nil, fmt.Errorf("rimsky_wait_set list for receiver: %w", err)
	}
	defer rows.Close()
	return collectWaitSetRows(rows)
}

func (b *waitSetImpl) ListForFrame(ctx context.Context, frameID shared.UUID, tx persistence.Tx) ([]persistence.WaitSetRow, error) {
	rows, err := b.q(tx).QueryContext(ctx,
		`SELECT frame_id, receiver_run_id, sender_run_id, topic_kind, topic_filter, drained_at
		   FROM rimsky_wait_set
		  WHERE frame_id = ?`,
		frameID)
	if err != nil {
		return nil, fmt.Errorf("rimsky_wait_set list for frame: %w", err)
	}
	defer rows.Close()
	return collectWaitSetRows(rows)
}

// @concept: cascade
// @decision: walker-rule-per-sender-node
func (b *waitSetImpl) ListSenderNodesForReceiver(
	ctx context.Context, frameID, receiverNodeRunID shared.UUID, tx persistence.Tx,
) ([]shared.UUID, error) {
	rows, err := b.q(tx).QueryContext(ctx,
		`SELECT DISTINCT r.node_id
		   FROM rimsky_wait_set w
		   JOIN rimsky_node_runs r ON r.id = w.sender_run_id
		  WHERE w.frame_id = ? AND w.receiver_run_id = ?`,
		frameID, receiverNodeRunID,
	)
	if err != nil {
		return nil, fmt.Errorf("ListSenderNodesForReceiver: %w", err)
	}
	defer rows.Close()
	var out []shared.UUID
	for rows.Next() {
		var idStr string
		if err := rows.Scan(&idStr); err != nil {
			return nil, fmt.Errorf("ListSenderNodesForReceiver: scan: %w", err)
		}
		id, err := parseUUID(idStr)
		if err != nil {
			return nil, fmt.Errorf("ListSenderNodesForReceiver: parse: %w", err)
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
	var n int
	err := b.q(tx).QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rimsky_wait_set
		  WHERE frame_id = ? AND receiver_run_id = ? AND sender_run_id = ?`,
		frameID, receiverNodeRunID, senderNodeRunID,
	).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("HasRowForSenderRun: %w", err)
	}
	return n > 0, nil
}

// @concept: cascade
func (b *waitSetImpl) ListPendingReceiversForDrainedSender(
	ctx context.Context, frameID, senderNodeRunID shared.UUID, tx persistence.Tx,
) ([]shared.UUID, error) {
	rows, err := b.q(tx).QueryContext(ctx,
		`SELECT DISTINCT w.receiver_run_id
		   FROM rimsky_wait_set w
		   JOIN rimsky_node_runs r ON r.id = w.receiver_run_id
		  WHERE w.frame_id = ? AND w.sender_run_id = ?
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
		var idStr string
		if err := rows.Scan(&idStr); err != nil {
			return nil, fmt.Errorf("ListPendingReceiversForDrainedSender: scan: %w", err)
		}
		id, err := parseUUID(idStr)
		if err != nil {
			return nil, fmt.Errorf("ListPendingReceiversForDrainedSender: parse: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// @concept: cascade
func (b *waitSetImpl) HasUndrainedRowsForReceiver(
	ctx context.Context, frameID, receiverNodeRunID shared.UUID, tx persistence.Tx,
) (bool, error) {
	var n int
	err := b.q(tx).QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rimsky_wait_set
		  WHERE frame_id = ? AND receiver_run_id = ? AND drained_at IS NULL`,
		frameID, receiverNodeRunID,
	).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("HasUndrainedRowsForReceiver: %w", err)
	}
	return n > 0, nil
}

func collectWaitSetRows(rows *sql.Rows) ([]persistence.WaitSetRow, error) {
	out := []persistence.WaitSetRow{}
	for rows.Next() {
		var w persistence.WaitSetRow
		var filter sql.NullString
		var drainedAtStr sql.NullString
		if err := rows.Scan(&w.FrameID, &w.ReceiverNodeRunID, &w.SenderNodeRunID,
			&w.TopicKind, &filter, &drainedAtStr); err != nil {
			return nil, err
		}
		if filter.Valid && filter.String != "" {
			w.TopicFilter = json.RawMessage(filter.String)
		}
		if drainedAtStr.Valid && drainedAtStr.String != "" {
			t, err := parseTime(drainedAtStr.String)
			if err != nil {
				return nil, fmt.Errorf("rimsky_wait_set parse drained_at: %w", err)
			}
			w.DrainedAt = &t
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
