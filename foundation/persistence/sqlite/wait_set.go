// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// SQLite impl of persistence.WaitSetTable — mirrors the postgres impl
// under wait_set.go. UUID columns are TEXT and JSONB is TEXT.
//
//	@concept: wait-set
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
)

// WaitSet returns the sqlite WaitSetTable impl.
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
		   (frame_id, receiver_node_id, sender_node_id, topic_kind, subscription_scope, topic_filter)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (frame_id, receiver_node_id, sender_node_id, topic_kind, subscription_scope)
		 DO NOTHING`,
		row.FrameID, row.ReceiverNodeID, row.SenderNodeID,
		row.TopicKind, row.SubscriptionScope, filter)
	if err != nil {
		return fmt.Errorf("rimsky_wait_set insert: %w", err)
	}
	return nil
}

func (b *waitSetImpl) DeleteBySender(ctx context.Context, frameID, senderID shared.UUID, tx persistence.Tx) error {
	_, err := b.q(tx).ExecContext(ctx,
		`DELETE FROM rimsky_wait_set
		  WHERE frame_id = ? AND sender_node_id = ?`,
		frameID, senderID)
	if err != nil {
		return fmt.Errorf("rimsky_wait_set delete by sender: %w", err)
	}
	return nil
}

func (b *waitSetImpl) ListForReceiver(ctx context.Context, frameID, receiverID shared.UUID, tx persistence.Tx) ([]persistence.WaitSetRow, error) {
	rows, err := b.q(tx).QueryContext(ctx,
		`SELECT frame_id, receiver_node_id, sender_node_id, topic_kind, subscription_scope, topic_filter
		   FROM rimsky_wait_set
		  WHERE frame_id = ? AND receiver_node_id = ?`,
		frameID, receiverID)
	if err != nil {
		return nil, fmt.Errorf("rimsky_wait_set list for receiver: %w", err)
	}
	defer rows.Close()
	return collectWaitSetRows(rows)
}

func (b *waitSetImpl) ListForFrame(ctx context.Context, frameID shared.UUID, tx persistence.Tx) ([]persistence.WaitSetRow, error) {
	rows, err := b.q(tx).QueryContext(ctx,
		`SELECT frame_id, receiver_node_id, sender_node_id, topic_kind, subscription_scope, topic_filter
		   FROM rimsky_wait_set
		  WHERE frame_id = ?`,
		frameID)
	if err != nil {
		return nil, fmt.Errorf("rimsky_wait_set list for frame: %w", err)
	}
	defer rows.Close()
	return collectWaitSetRows(rows)
}

func collectWaitSetRows(rows *sql.Rows) ([]persistence.WaitSetRow, error) {
	out := []persistence.WaitSetRow{}
	for rows.Next() {
		var w persistence.WaitSetRow
		var filter sql.NullString
		if err := rows.Scan(&w.FrameID, &w.ReceiverNodeID, &w.SenderNodeID,
			&w.TopicKind, &w.SubscriptionScope, &filter); err != nil {
			return nil, err
		}
		if filter.Valid && filter.String != "" {
			w.TopicFilter = json.RawMessage(filter.String)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
