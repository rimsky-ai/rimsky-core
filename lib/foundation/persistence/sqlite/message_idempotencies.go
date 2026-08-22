// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

type messageIdempotenciesImpl tablesImpl

var _ persistence.MessageIdempotencyTable = (*messageIdempotenciesImpl)(nil)

func (s *tablesImpl) MessageIdempotencies() persistence.MessageIdempotencyTable {
	return (*messageIdempotenciesImpl)(s)
}

func (b *messageIdempotenciesImpl) q(tx persistence.Tx) querier { return (*tablesImpl)(b).q(tx) }

const sqliteInsertMessageIdempotencySQL = `
INSERT INTO rimsky_message_idempotencies (
    instance_id, sender_kind, sender, sender_subject, idempotency_key, message_id, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (instance_id, sender_kind, sender, sender_subject, idempotency_key) DO NOTHING`

const sqliteSelectMessageIdempotencySQL = `
SELECT message_id, created_at
  FROM rimsky_message_idempotencies
 WHERE instance_id = ? AND sender_kind = ? AND sender = ? AND sender_subject = ? AND idempotency_key = ?`

func (b *messageIdempotenciesImpl) InsertOrLookup(ctx context.Context, row persistence.MessageIdempotencyRow, tx persistence.Tx) (persistence.MessageIdempotencyRow, bool, error) {
	// @decision: message-sender-kind-discriminator
	if err := row.ValidateSenderKind(); err != nil {
		return persistence.MessageIdempotencyRow{}, false, err
	}
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now().UTC()
	}
	res, err := b.q(tx).ExecContext(ctx, sqliteInsertMessageIdempotencySQL,
		row.InstanceID.String(), row.SenderKind, row.Sender, row.SenderSubject, row.IdempotencyKey,
		row.MessageID.String(), formatTime(row.CreatedAt))
	if err != nil {
		return persistence.MessageIdempotencyRow{}, false, fmt.Errorf("sqlite.MessageIdempotencies.Insert: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return persistence.MessageIdempotencyRow{}, false, fmt.Errorf("sqlite.MessageIdempotencies.RowsAffected: %w", err)
	}
	if n == 1 {
		return row, true, nil
	}
	var msgIDStr, createdAtStr string
	err = b.q(tx).QueryRowContext(ctx, sqliteSelectMessageIdempotencySQL,
		row.InstanceID.String(), row.SenderKind, row.Sender, row.SenderSubject, row.IdempotencyKey,
	).Scan(&msgIDStr, &createdAtStr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return persistence.MessageIdempotencyRow{}, false, fmt.Errorf("sqlite.MessageIdempotencies.Lookup: row vanished after conflict")
		}
		return persistence.MessageIdempotencyRow{}, false, fmt.Errorf("sqlite.MessageIdempotencies.Lookup: %w", err)
	}
	msgID, err := uuid.Parse(msgIDStr)
	if err != nil {
		return persistence.MessageIdempotencyRow{}, false, fmt.Errorf("sqlite.MessageIdempotencies.Lookup: bad message_id %q: %w", msgIDStr, err)
	}
	t, err := parseTime(createdAtStr)
	if err != nil {
		return persistence.MessageIdempotencyRow{}, false, fmt.Errorf("sqlite.MessageIdempotencies.Lookup: bad created_at %q: %w", createdAtStr, err)
	}
	return persistence.MessageIdempotencyRow{
		InstanceID:     row.InstanceID,
		SenderKind:     row.SenderKind,
		Sender:         row.Sender,
		SenderSubject:  row.SenderSubject,
		IdempotencyKey: row.IdempotencyKey,
		MessageID:      msgID,
		CreatedAt:      t,
	}, false, nil
}

const sqliteDeleteMessageIdempotenciesOlderThanSQL = `
DELETE FROM rimsky_message_idempotencies WHERE created_at < ?`

func (b *messageIdempotenciesImpl) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := (*tablesImpl)(b).db.ExecContext(ctx, sqliteDeleteMessageIdempotenciesOlderThanSQL,
		formatTime(cutoff))
	if err != nil {
		return 0, fmt.Errorf("sqlite.MessageIdempotencies.DeleteOlderThan: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sqlite.MessageIdempotencies.DeleteOlderThan.RowsAffected: %w", err)
	}
	return n, nil
}
