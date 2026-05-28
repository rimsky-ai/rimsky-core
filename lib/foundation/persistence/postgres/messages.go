// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Postgres impl of persistence.MessagesTable — the unified message
// queue per spec §Unified message layer.
//
// V1 implementation: the schema is in place (baseline migration) but the
// rimsky-side runtime that delivers messages at frame boundaries (E5)
// is deferred to a follow-up dispatch. The accessor methods below
// implement the interface so the build is clean; tests exercise the
// happy path (Insert + ListPending + MarkDelivered). The frame-
// boundary delivery loop in runtime/message_delivery.go is the next
// dispatch's task.

package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type messagesImpl tablesImpl

var _ persistence.MessagesTable = (*messagesImpl)(nil)

// Messages returns the postgres MessagesTable impl.
func (s *tablesImpl) Messages() persistence.MessagesTable { return (*messagesImpl)(s) }

func (b *messagesImpl) q(tx persistence.Tx) querier { return (*tablesImpl)(b).q(tx) }

const insertMessageSQL = `
INSERT INTO rimsky_messages (
    id, instance_id, kind, sender, sender_kind, target, payload,
    backfill_operation_id, received_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

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
		backfill = *req.BackfillOperationID
	}
	_, err := b.q(tx).Exec(ctx, insertMessageSQL,
		req.ID, req.InstanceID, req.Kind, req.Sender, req.SenderKind,
		target, req.Payload, backfill, req.ReceivedAt)
	if err != nil {
		return fmt.Errorf("postgres.Messages.Insert: %w", err)
	}
	return nil
}

const markDeliveredSQL = `
UPDATE rimsky_messages
   SET delivered_at = $1, frame_id = $2
 WHERE id = $3 AND delivered_at IS NULL`

func (b *messagesImpl) MarkDelivered(ctx context.Context, tx persistence.Tx, id shared.UUID, frame shared.UUID, deliveredAt time.Time) (bool, error) {
	tag, err := b.q(tx).Exec(ctx, markDeliveredSQL, deliveredAt, frame, id)
	if err != nil {
		return false, fmt.Errorf("postgres.Messages.MarkDelivered: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

const markCancelledSQL = `
UPDATE rimsky_messages
   SET cancelled = TRUE, delivered_at = $1, frame_id = NULL
 WHERE backfill_operation_id = $2 AND delivered_at IS NULL`

func (b *messagesImpl) MarkCancelled(ctx context.Context, tx persistence.Tx, backfillOperationID shared.UUID, at time.Time) (int, error) {
	tag, err := b.q(tx).Exec(ctx, markCancelledSQL, at, backfillOperationID)
	if err != nil {
		return 0, fmt.Errorf("postgres.Messages.MarkCancelled: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

const listPendingForInstanceSQL = `
SELECT id, instance_id, kind, sender, sender_kind, target, payload,
       backfill_operation_id, received_at, delivered_at, frame_id, cancelled
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

const getMessageSQL = `
SELECT id, instance_id, kind, sender, sender_kind, target, payload,
       backfill_operation_id, received_at, delivered_at, frame_id, cancelled
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

// List is a paginated list with filter. V1 implementation supports
// instance_id + kind + sender_kind + target + backfill_operation_id
// filters; cursor pagination follows received_at DESC.
func (b *messagesImpl) List(ctx context.Context, filter persistence.MessageListFilter, pag persistence.ListPagination) (persistence.PaginatedListResult[persistence.MessageRow], error) {
	// V1 implementation: full-scan filter; cursor pagination is a
	// follow-up. Returns at most pag.Limit rows.
	args := []any{}
	where := "WHERE TRUE"
	if filter.InstanceID != nil {
		args = append(args, *filter.InstanceID)
		where += fmt.Sprintf(" AND instance_id = $%d", len(args))
	}
	if filter.Kind != "" {
		args = append(args, filter.Kind)
		where += fmt.Sprintf(" AND kind = $%d", len(args))
	}
	if filter.SenderKind != "" {
		args = append(args, filter.SenderKind)
		where += fmt.Sprintf(" AND sender_kind = $%d", len(args))
	}
	if filter.Target != "" {
		args = append(args, filter.Target)
		where += fmt.Sprintf(" AND target = $%d", len(args))
	}
	if filter.BackfillOperationID != nil {
		args = append(args, *filter.BackfillOperationID)
		where += fmt.Sprintf(" AND backfill_operation_id = $%d", len(args))
	}
	limit := pag.Limit
	if limit <= 0 {
		limit = 100
	}
	args = append(args, limit)
	sql := fmt.Sprintf(`SELECT id, instance_id, kind, sender, sender_kind, target, payload,
       backfill_operation_id, received_at, delivered_at, frame_id, cancelled
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
	return persistence.PaginatedListResult[persistence.MessageRow]{Rows: out}, nil
}

func collectMessages(rows pgx.Rows) ([]persistence.MessageRow, error) {
	out := []persistence.MessageRow{}
	for rows.Next() {
		var m persistence.MessageRow
		var target *string
		var deliveredAt *time.Time
		var frameID *shared.UUID
		var backfill *shared.UUID
		if err := rows.Scan(
			&m.ID, &m.InstanceID, &m.Kind, &m.Sender, &m.SenderKind,
			&target, &m.Payload, &backfill, &m.ReceivedAt, &deliveredAt,
			&frameID, &m.Cancelled,
		); err != nil {
			return nil, err
		}
		if target != nil {
			m.Target = *target
		}
		m.DeliveredAt = deliveredAt
		m.FrameID = frameID
		m.BackfillOperationID = backfill
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ErrMessagesNotWired is the sentinel reserved for paths that the V1
// schema admits but the V1 runtime has not yet exercised end-to-end.
// Kept here so future callers can errors.Is() check.
var ErrMessagesNotWired = errors.New("messages: runtime path not wired")
