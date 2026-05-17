// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// SQLite impl of persistence.MessagesTable — mirror of the postgres
// impl. SQLite is dev-only; multi-host deployments must use postgres.

package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
)

type messagesImpl tablesImpl

var _ persistence.MessagesTable = (*messagesImpl)(nil)

func (s *tablesImpl) Messages() persistence.MessagesTable { return (*messagesImpl)(s) }

func (b *messagesImpl) q(tx persistence.Tx) querier { return (*tablesImpl)(b).q(tx) }

const sqliteInsertMessageSQL = `
INSERT INTO rimsky_messages (
    id, instance_id, kind, sender, sender_kind, target, payload,
    backfill_operation_id, received_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

func (b *messagesImpl) Insert(ctx context.Context, tx persistence.Tx, req persistence.EnqueueMessageRequest) error {
	if req.ReceivedAt.IsZero() {
		req.ReceivedAt = time.Now().UTC()
	}
	var target any
	if req.Target != "" {
		target = req.Target
	}
	var backfill any
	if req.BackfillOperationID != nil {
		backfill = req.BackfillOperationID.String()
	}
	_, err := b.q(tx).ExecContext(ctx, sqliteInsertMessageSQL,
		req.ID.String(), req.InstanceID.String(), req.Kind, req.Sender,
		req.SenderKind, target, req.Payload, backfill, req.ReceivedAt)
	if err != nil {
		return fmt.Errorf("sqlite.Messages.Insert: %w", err)
	}
	return nil
}

const sqliteMarkDeliveredSQL = `
UPDATE rimsky_messages
   SET delivered_at = ?, frame_id = ?
 WHERE id = ? AND delivered_at IS NULL`

func (b *messagesImpl) MarkDelivered(ctx context.Context, tx persistence.Tx, id shared.UUID, frame shared.UUID, deliveredAt time.Time) (bool, error) {
	res, err := b.q(tx).ExecContext(ctx, sqliteMarkDeliveredSQL, deliveredAt, frame.String(), id.String())
	if err != nil {
		return false, fmt.Errorf("sqlite.Messages.MarkDelivered: %w", err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

const sqliteMarkCancelledSQL = `
UPDATE rimsky_messages
   SET cancelled = 1, delivered_at = ?, frame_id = NULL
 WHERE backfill_operation_id = ? AND delivered_at IS NULL`

func (b *messagesImpl) MarkCancelled(ctx context.Context, tx persistence.Tx, backfillOperationID shared.UUID, at time.Time) (int, error) {
	res, err := b.q(tx).ExecContext(ctx, sqliteMarkCancelledSQL, at, backfillOperationID.String())
	if err != nil {
		return 0, fmt.Errorf("sqlite.Messages.MarkCancelled: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

const sqliteListPendingMessagesSQL = `
SELECT id, instance_id, kind, sender, sender_kind, target, payload,
       backfill_operation_id, received_at, delivered_at, frame_id, cancelled
  FROM rimsky_messages
 WHERE instance_id = ? AND delivered_at IS NULL AND cancelled = 0
 ORDER BY received_at ASC, id ASC`

func (b *messagesImpl) ListPendingForInstance(ctx context.Context, tx persistence.Tx, instanceID shared.UUID) ([]persistence.MessageRow, error) {
	rows, err := b.q(tx).QueryContext(ctx, sqliteListPendingMessagesSQL, instanceID.String())
	if err != nil {
		return nil, fmt.Errorf("sqlite.Messages.ListPendingForInstance: %w", err)
	}
	defer rows.Close()
	return scanMessages(rows)
}

const sqliteGetMessageSQL = `
SELECT id, instance_id, kind, sender, sender_kind, target, payload,
       backfill_operation_id, received_at, delivered_at, frame_id, cancelled
  FROM rimsky_messages
 WHERE id = ?`

func (b *messagesImpl) Get(ctx context.Context, id shared.UUID) (*persistence.MessageRow, error) {
	rows, err := (*tablesImpl)(b).db.QueryContext(ctx, sqliteGetMessageSQL, id.String())
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
	if filter.Kind != "" {
		args = append(args, filter.Kind)
		conds = append(conds, "kind = ?")
	}
	if filter.SenderKind != "" {
		args = append(args, filter.SenderKind)
		conds = append(conds, "sender_kind = ?")
	}
	if filter.Target != "" {
		args = append(args, filter.Target)
		conds = append(conds, "target = ?")
	}
	if filter.BackfillOperationID != nil {
		args = append(args, filter.BackfillOperationID.String())
		conds = append(conds, "backfill_operation_id = ?")
	}
	limit := pag.Limit
	if limit <= 0 {
		limit = 100
	}
	args = append(args, limit)
	sql := fmt.Sprintf(`SELECT id, instance_id, kind, sender, sender_kind, target, payload,
       backfill_operation_id, received_at, delivered_at, frame_id, cancelled
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
	return persistence.PaginatedListResult[persistence.MessageRow]{Rows: out}, nil
}

func scanMessages(rows *sql.Rows) ([]persistence.MessageRow, error) {
	out := []persistence.MessageRow{}
	for rows.Next() {
		var m persistence.MessageRow
		var idStr, instanceStr string
		var target sql.NullString
		var backfillStr sql.NullString
		var frameStr sql.NullString
		var deliveredAt sql.NullTime
		var cancelled int
		if err := rows.Scan(
			&idStr, &instanceStr, &m.Kind, &m.Sender, &m.SenderKind,
			&target, &m.Payload, &backfillStr, &m.ReceivedAt, &deliveredAt,
			&frameStr, &cancelled,
		); err != nil {
			return nil, err
		}
		if u, err := uuid.Parse(idStr); err == nil {
			m.ID = u
		}
		if u, err := uuid.Parse(instanceStr); err == nil {
			m.InstanceID = u
		}
		if target.Valid {
			m.Target = target.String
		}
		if backfillStr.Valid {
			if u, err := uuid.Parse(backfillStr.String); err == nil {
				m.BackfillOperationID = &u
			}
		}
		if frameStr.Valid {
			if u, err := uuid.Parse(frameStr.String); err == nil {
				m.FrameID = &u
			}
		}
		if deliveredAt.Valid {
			t := deliveredAt.Time
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
