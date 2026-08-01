// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package sqlite

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type messagesImpl tablesImpl

var _ persistence.MessageTable = (*messagesImpl)(nil)

func (s *tablesImpl) Messages() persistence.MessageTable { return (*messagesImpl)(s) }

func (b *messagesImpl) q(tx persistence.Tx) querier { return (*tablesImpl)(b).q(tx) }

const sqliteInsertMessageSQL = `
INSERT INTO rimsky_messages (
    id, instance_id, type, sender, sender_kind, payload, received_at
) VALUES (?, ?, ?, ?, ?, ?, ?)`

func (b *messagesImpl) Insert(ctx context.Context, req persistence.EnqueueMessageRequest, tx persistence.Tx) error {
	if req.ReceivedAt.IsZero() {
		req.ReceivedAt = time.Now().UTC()
	}
	_, err := b.q(tx).ExecContext(ctx, sqliteInsertMessageSQL,
		req.ID.String(), req.InstanceID.String(), req.Type, req.Sender,
		req.SenderKind, req.Payload, formatTime(req.ReceivedAt))
	if err != nil {
		return fmt.Errorf("sqlite.Messages.Insert: %w", err)
	}
	return nil
}

const sqliteMarkDeliveredSQL = `
UPDATE rimsky_messages
   SET delivered_at = ?, frame_id = ?
 WHERE id = ? AND delivered_at IS NULL AND cancelled = 0`

func (b *messagesImpl) MarkDelivered(ctx context.Context, id shared.UUID, frame shared.UUID, deliveredAt time.Time, tx persistence.Tx) (bool, error) {
	res, err := b.q(tx).ExecContext(ctx, sqliteMarkDeliveredSQL, formatTime(deliveredAt), frame.String(), id.String())
	if err != nil {
		return false, fmt.Errorf("sqlite.Messages.MarkDelivered: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("sqlite.Messages.MarkDelivered.rowsAffected: %w", err)
	}
	return n == 1, nil
}

const sqliteListPendingMessagesSQL = `
SELECT id, instance_id, type, sender, sender_kind, payload,
       received_at, delivered_at, frame_id, cancelled
  FROM rimsky_messages
 WHERE instance_id = ? AND delivered_at IS NULL AND cancelled = 0
 ORDER BY received_at ASC, id ASC`

func (b *messagesImpl) ListPendingForInstance(ctx context.Context, instanceID shared.UUID, tx persistence.Tx) ([]persistence.MessageRow, error) {
	rows, err := b.q(tx).QueryContext(ctx, sqliteListPendingMessagesSQL, instanceID.String())
	if err != nil {
		return nil, fmt.Errorf("sqlite.Messages.ListPendingForInstance: %w", err)
	}
	defer rows.Close()
	return scanMessages(rows)
}

const sqliteListDeliveredForFrameSQL = `
SELECT id, instance_id, type, sender, sender_kind, payload,
       received_at, delivered_at, frame_id, cancelled
  FROM rimsky_messages
 WHERE frame_id = ?
 ORDER BY received_at ASC, id ASC`

func (b *messagesImpl) ListDeliveredForFrame(ctx context.Context, frame shared.UUID, tx persistence.Tx) ([]persistence.MessageRow, error) {
	rows, err := b.q(tx).QueryContext(ctx, sqliteListDeliveredForFrameSQL, frame.String())
	if err != nil {
		return nil, fmt.Errorf("sqlite.Messages.ListDeliveredForFrame: %w", err)
	}
	defer rows.Close()
	return scanMessages(rows)
}

const sqliteGetMessageSQL = `
SELECT id, instance_id, type, sender, sender_kind, payload,
       received_at, delivered_at, frame_id, cancelled
  FROM rimsky_messages
 WHERE id = ?`

func (b *messagesImpl) Get(ctx context.Context, id shared.UUID, tx persistence.Tx) (*persistence.MessageRow, error) {
	rows, err := b.q(tx).QueryContext(ctx, sqliteGetMessageSQL, id.String())
	if err != nil {
		return nil, fmt.Errorf("sqlite.Messages.Get: %w", err)
	}
	defer rows.Close()
	out, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	return &out[0], nil
}

func (b *messagesImpl) List(ctx context.Context, filter persistence.MessageListFilter, pag persistence.ListPagination) (persistence.PaginatedListResult[persistence.MessageRow], error) {
	args := []any{}
	conds := []string{"1 = 1"}
	if filter.InstanceID != nil {
		args = append(args, filter.InstanceID.String())
		conds = append(conds, "instance_id = ?")
	}
	if filter.Type != nil {
		args = append(args, *filter.Type)
		conds = append(conds, "type = ?")
	}
	if filter.Sender != "" {
		args = append(args, filter.Sender)
		conds = append(conds, "sender = ?")
	}
	if filter.SenderKind != nil {
		args = append(args, *filter.SenderKind)
		conds = append(conds, "sender_kind = ?")
	}
	if filter.FrameID != nil {
		args = append(args, filter.FrameID.String())
		conds = append(conds, "frame_id = ?")
	}
	if filter.DeliveredAfter != nil {
		args = append(args, formatTime(*filter.DeliveredAfter))
		conds = append(conds, "delivered_at > ?")
	}
	if filter.DeliveredBefore != nil {
		args = append(args, formatTime(*filter.DeliveredBefore))
		conds = append(conds, "delivered_at < ?")
	}
	if filter.Pending != nil {
		if *filter.Pending {
			conds = append(conds, "delivered_at IS NULL AND cancelled = 0")
		} else {
			conds = append(conds, "(delivered_at IS NOT NULL OR cancelled = 1)")
		}
	}
	if pag.Cursor != "" {
		cursorReceived, cursorID, err := decodeMessageCursor(pag.Cursor)
		if err != nil {
			return persistence.PaginatedListResult[persistence.MessageRow]{}, fmt.Errorf("sqlite.Messages.List: bad cursor: %w", err)
		}
		args = append(args, formatTime(cursorReceived), cursorID.String())
		conds = append(conds, "(received_at, id) < (?, ?)")
	}
	limit := pag.Limit
	if limit <= 0 {
		limit = 100
	}
	args = append(args, limit)
	sql := fmt.Sprintf(`SELECT id, instance_id, type, sender, sender_kind, payload,
       received_at, delivered_at, frame_id, cancelled
  FROM rimsky_messages
 WHERE %s
 ORDER BY received_at DESC, id DESC
 LIMIT ?`, strings.Join(conds, " AND "))
	rows, err := (*tablesImpl)(b).db.QueryContext(ctx, sql, args...)
	if err != nil {
		return persistence.PaginatedListResult[persistence.MessageRow]{}, fmt.Errorf("sqlite.Messages.List: %w", err)
	}
	defer rows.Close()
	out, err := scanMessages(rows)
	if err != nil {
		return persistence.PaginatedListResult[persistence.MessageRow]{}, err
	}
	var nextCursor string
	if len(out) == limit && len(out) > 0 {
		last := out[len(out)-1]
		nextCursor = encodeMessageCursor(last.ReceivedAt, last.ID)
	}
	return persistence.PaginatedListResult[persistence.MessageRow]{Rows: out, NextCursor: nextCursor}, nil
}

type messageCursor struct {
	R time.Time `json:"r"`
	I string    `json:"i"`
}

func encodeMessageCursor(receivedAt time.Time, id shared.UUID) string {
	c := messageCursor{R: receivedAt, I: id.String()}
	b, _ := json.Marshal(c)
	return base64.StdEncoding.EncodeToString(b)
}

func decodeMessageCursor(s string) (time.Time, shared.UUID, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, shared.UUID{}, err
	}
	var c messageCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return time.Time{}, shared.UUID{}, err
	}
	id, err := uuid.Parse(c.I)
	if err != nil {
		return time.Time{}, shared.UUID{}, err
	}
	return c.R, shared.UUID(id), nil
}

const sqliteCancelPendingForInstanceSQL = `
UPDATE rimsky_messages
   SET cancelled = 1
 WHERE instance_id = ? AND delivered_at IS NULL AND cancelled = 0`

func (b *messagesImpl) CancelPendingForInstance(ctx context.Context, instanceID shared.UUID, tx persistence.Tx) (int, error) {
	res, err := b.q(tx).ExecContext(ctx, sqliteCancelPendingForInstanceSQL, instanceID.String())
	if err != nil {
		return 0, fmt.Errorf("sqlite.Messages.CancelPendingForInstance: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

const sqlitePickPendingForIdleInstancesSQL = `
WITH ranked AS (
    SELECT m.id, m.instance_id,
           ROW_NUMBER() OVER (
               PARTITION BY m.instance_id
               ORDER BY m.received_at ASC, m.id ASC
           ) AS rn
      FROM rimsky_messages m
      JOIN rimsky_instances i ON i.id = m.instance_id
     WHERE m.delivered_at IS NULL
       AND m.cancelled = 0
       AND i.terminated_at IS NULL
       AND i.paused = 0
       AND NOT EXISTS (
           SELECT 1 FROM rimsky_frames f
            WHERE f.instance_id = m.instance_id
              AND f.ended_at IS NULL
       )
)
SELECT instance_id, id FROM ranked WHERE rn = 1`

func (b *messagesImpl) PickPendingMessagesForIdleInstances(ctx context.Context, tx persistence.Tx) ([]persistence.PendingMessagePick, error) {
	rows, err := b.q(tx).QueryContext(ctx, sqlitePickPendingForIdleInstancesSQL)
	if err != nil {
		return nil, fmt.Errorf("sqlite.Messages.PickPendingMessagesForIdleInstances: %w", err)
	}
	defer rows.Close()
	var out []persistence.PendingMessagePick
	for rows.Next() {
		var instanceStr, messageStr string
		if err := rows.Scan(&instanceStr, &messageStr); err != nil {
			return nil, fmt.Errorf("sqlite.Messages.PickPendingMessagesForIdleInstances: scan: %w", err)
		}
		iid, err := uuid.Parse(instanceStr)
		if err != nil {
			return nil, err
		}
		mid, err := uuid.Parse(messageStr)
		if err != nil {
			return nil, err
		}
		out = append(out, persistence.PendingMessagePick{InstanceID: iid, MessageID: mid})
	}
	return out, rows.Err()
}

func scanMessages(rows *sql.Rows) ([]persistence.MessageRow, error) {
	out := []persistence.MessageRow{}
	for rows.Next() {
		var m persistence.MessageRow
		var idStr, instanceStr string
		var frameStr sql.NullString
		var receivedAtStr string
		var deliveredAtStr sql.NullString
		var cancelled int
		var payload []byte
		if err := rows.Scan(
			&idStr, &instanceStr, &m.Type, &m.Sender, &m.SenderKind,
			&payload, &receivedAtStr, &deliveredAtStr,
			&frameStr, &cancelled,
		); err != nil {
			return nil, err
		}
		receivedAt, err := parseTime(receivedAtStr)
		if err != nil {
			return nil, fmt.Errorf("sqlite.Messages: parse received_at: %w", err)
		}
		m.ReceivedAt = receivedAt
		if payload != nil {
			m.Payload = payload
		}
		u, err := uuid.Parse(idStr)
		if err != nil {
			return nil, fmt.Errorf("sqlite.Messages: parse id: %w", err)
		}
		m.ID = u
		if u, err = uuid.Parse(instanceStr); err != nil {
			return nil, fmt.Errorf("sqlite.Messages: parse instance_id: %w", err)
		}
		m.InstanceID = u
		if frameStr.Valid {
			if u, err = uuid.Parse(frameStr.String); err != nil {
				return nil, fmt.Errorf("sqlite.Messages: parse frame_id: %w", err)
			}
			m.FrameID = &u
		}
		if deliveredAtStr.Valid {
			t, err := parseTime(deliveredAtStr.String)
			if err != nil {
				return nil, fmt.Errorf("sqlite.Messages: parse delivered_at: %w", err)
			}
			m.DeliveredAt = &t
		}
		m.Cancelled = cancelled != 0
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
