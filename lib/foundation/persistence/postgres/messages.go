// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type messagesImpl tablesImpl

var _ persistence.MessageTable = (*messagesImpl)(nil)

func (s *tablesImpl) Messages() persistence.MessageTable { return (*messagesImpl)(s) }

func (b *messagesImpl) q(tx persistence.Tx) querier { return (*tablesImpl)(b).q(tx) }

const insertMessageSQL = `
INSERT INTO rimsky_messages (
    id, instance_id, type, sender, sender_kind, payload, received_at
) VALUES ($1, $2, $3, $4, $5, $6, $7)`

func (b *messagesImpl) Insert(ctx context.Context, tx persistence.Tx, req persistence.EnqueueMessageRequest) error {
	if req.ReceivedAt.IsZero() {
		req.ReceivedAt = time.Now().UTC()
	}
	_, err := b.q(tx).Exec(ctx, insertMessageSQL,
		req.ID, req.InstanceID, req.Type, req.Sender, req.SenderKind,
		req.Payload, req.ReceivedAt)
	if err != nil {
		return fmt.Errorf("postgres.Messages.Insert: %w", err)
	}
	return nil
}

const markDeliveredSQL = `
UPDATE rimsky_messages
   SET delivered_at = $1, frame_id = $2
 WHERE id = $3 AND delivered_at IS NULL AND cancelled = FALSE`

func (b *messagesImpl) MarkDelivered(ctx context.Context, tx persistence.Tx, id shared.UUID, frame shared.UUID, deliveredAt time.Time) (bool, error) {
	tag, err := b.q(tx).Exec(ctx, markDeliveredSQL, deliveredAt, frame, id)
	if err != nil {
		return false, fmt.Errorf("postgres.Messages.MarkDelivered: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

const listPendingForInstanceSQL = `
SELECT id, instance_id, type, sender, sender_kind, payload,
       received_at, delivered_at, frame_id, cancelled
  FROM rimsky_messages
 WHERE instance_id = $1 AND delivered_at IS NULL AND cancelled = FALSE
 ORDER BY received_at ASC, id ASC`

func (b *messagesImpl) ListPendingForInstance(ctx context.Context, tx persistence.Tx, instanceID shared.UUID) ([]persistence.MessageRow, error) {
	rows, err := b.q(tx).Query(ctx, listPendingForInstanceSQL, instanceID)
	if err != nil {
		return nil, fmt.Errorf("postgres.Messages.ListPendingForInstance: %w", err)
	}
	defer rows.Close()
	return collectMessages(rows)
}

const listDeliveredForFrameSQL = `
SELECT id, instance_id, type, sender, sender_kind, payload,
       received_at, delivered_at, frame_id, cancelled
  FROM rimsky_messages
 WHERE frame_id = $1
 ORDER BY received_at ASC, id ASC`

func (b *messagesImpl) ListDeliveredForFrame(ctx context.Context, tx persistence.Tx, frame shared.UUID) ([]persistence.MessageRow, error) {
	rows, err := b.q(tx).Query(ctx, listDeliveredForFrameSQL, frame)
	if err != nil {
		return nil, fmt.Errorf("postgres.Messages.ListDeliveredForFrame: %w", err)
	}
	defer rows.Close()
	return collectMessages(rows)
}

const getMessageSQL = `
SELECT id, instance_id, type, sender, sender_kind, payload,
       received_at, delivered_at, frame_id, cancelled
  FROM rimsky_messages
 WHERE id = $1`

func (b *messagesImpl) Get(ctx context.Context, id shared.UUID) (*persistence.MessageRow, error) {
	rows, err := (*tablesImpl)(b).pool.Query(ctx, getMessageSQL, id)
	if err != nil {
		return nil, fmt.Errorf("postgres.Messages.Get: %w", err)
	}
	defer rows.Close()
	out, err := collectMessages(rows)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	return &out[0], nil
}

func (b *messagesImpl) GetInTx(ctx context.Context, tx persistence.Tx, id shared.UUID) (*persistence.MessageRow, error) {
	rows, err := b.q(tx).Query(ctx, getMessageSQL, id)
	if err != nil {
		return nil, fmt.Errorf("postgres.Messages.GetInTx: %w", err)
	}
	defer rows.Close()
	out, err := collectMessages(rows)
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
	where := "WHERE TRUE"
	if filter.InstanceID != nil {
		args = append(args, *filter.InstanceID)
		where += fmt.Sprintf(" AND instance_id = $%d", len(args))
	}
	if filter.Type != nil {
		args = append(args, *filter.Type)
		where += fmt.Sprintf(" AND type = $%d", len(args))
	}
	if filter.Sender != "" {
		args = append(args, filter.Sender)
		where += fmt.Sprintf(" AND sender = $%d", len(args))
	}
	if filter.SenderKind != nil {
		args = append(args, *filter.SenderKind)
		where += fmt.Sprintf(" AND sender_kind = $%d", len(args))
	}
	if filter.FrameID != nil {
		args = append(args, *filter.FrameID)
		where += fmt.Sprintf(" AND frame_id = $%d", len(args))
	}
	if filter.DeliveredAfter != nil {
		args = append(args, *filter.DeliveredAfter)
		where += fmt.Sprintf(" AND delivered_at > $%d", len(args))
	}
	if filter.DeliveredBefore != nil {
		args = append(args, *filter.DeliveredBefore)
		where += fmt.Sprintf(" AND delivered_at < $%d", len(args))
	}
	if filter.Pending != nil {
		if *filter.Pending {
			where += " AND delivered_at IS NULL AND cancelled = FALSE"
		} else {
			where += " AND (delivered_at IS NOT NULL OR cancelled = TRUE)"
		}
	}
	if pag.Cursor != "" {
		cursorReceived, cursorID, err := decodeMessageCursor(pag.Cursor)
		if err != nil {
			return persistence.PaginatedListResult[persistence.MessageRow]{}, fmt.Errorf("postgres.Messages.List: bad cursor: %w", err)
		}
		args = append(args, cursorReceived, cursorID)
		where += fmt.Sprintf(" AND (received_at, id) < ($%d, $%d)", len(args)-1, len(args))
	}
	limit := pag.Limit
	if limit <= 0 {
		limit = 100
	}
	args = append(args, limit)
	sql := fmt.Sprintf(`SELECT id, instance_id, type, sender, sender_kind, payload,
       received_at, delivered_at, frame_id, cancelled
  FROM rimsky_messages %s
 ORDER BY received_at DESC, id DESC
 LIMIT $%d`, where, len(args))
	rows, err := (*tablesImpl)(b).pool.Query(ctx, sql, args...)
	if err != nil {
		return persistence.PaginatedListResult[persistence.MessageRow]{}, fmt.Errorf("postgres.Messages.List: %w", err)
	}
	defer rows.Close()
	out, err := collectMessages(rows)
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

const cancelPendingForInstanceSQL = `
UPDATE rimsky_messages
   SET cancelled = TRUE
 WHERE instance_id = $1 AND delivered_at IS NULL AND cancelled = FALSE`

func (b *messagesImpl) CancelPendingForInstance(ctx context.Context, tx persistence.Tx, instanceID shared.UUID) (int, error) {
	tag, err := b.q(tx).Exec(ctx, cancelPendingForInstanceSQL, instanceID)
	if err != nil {
		return 0, fmt.Errorf("postgres.Messages.CancelPendingForInstance: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

const pickPendingForIdleInstancesSQL = `
WITH ranked AS (
    SELECT m.id, m.instance_id,
           ROW_NUMBER() OVER (
               PARTITION BY m.instance_id
               ORDER BY m.received_at ASC, m.id ASC
           ) AS rn
      FROM rimsky_messages m
      JOIN rimsky_instances i ON i.id = m.instance_id
     WHERE m.delivered_at IS NULL
       AND m.cancelled = FALSE
       AND i.terminated_at IS NULL
       AND i.paused = FALSE
       AND NOT EXISTS (
           SELECT 1 FROM rimsky_frames f
            WHERE f.instance_id = m.instance_id
              AND f.ended_at IS NULL
       )
)
SELECT instance_id, id FROM ranked WHERE rn = 1`

func (b *messagesImpl) PickPendingMessagesForIdleInstances(ctx context.Context, tx persistence.Tx) ([]persistence.PendingMessagePick, error) {
	rows, err := b.q(tx).Query(ctx, pickPendingForIdleInstancesSQL)
	if err != nil {
		return nil, fmt.Errorf("postgres.Messages.PickPendingMessagesForIdleInstances: %w", err)
	}
	defer rows.Close()
	var out []persistence.PendingMessagePick
	for rows.Next() {
		var p persistence.PendingMessagePick
		if err := rows.Scan(&p.InstanceID, &p.MessageID); err != nil {
			return nil, fmt.Errorf("postgres.Messages.PickPendingMessagesForIdleInstances: scan: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func collectMessages(rows pgx.Rows) ([]persistence.MessageRow, error) {
	out := []persistence.MessageRow{}
	for rows.Next() {
		var m persistence.MessageRow
		var deliveredAt *time.Time
		var frameID *shared.UUID
		if err := rows.Scan(
			&m.ID, &m.InstanceID, &m.Type, &m.Sender, &m.SenderKind,
			&m.Payload, &m.ReceivedAt, &deliveredAt,
			&frameID, &m.Cancelled,
		); err != nil {
			return nil, err
		}
		m.DeliveredAt = deliveredAt
		m.FrameID = frameID
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
