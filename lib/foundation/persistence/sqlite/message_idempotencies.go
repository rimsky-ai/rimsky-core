// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// SQLite impl of persistence.MessageIdempotencyTable — mirror of the
// postgres impl. SQLite is dev-only.

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

// SQLite doesn't have postgres's xmax trick. We use two queries inside
// the caller's tx: first try INSERT … ON CONFLICT DO NOTHING; if no row
// was inserted, SELECT the existing row. The dedup tuple includes BOTH
// sender_kind (structural source-of-claim: operator/publisher/anonymous)
// and sender_subject (per-caller discriminator within operator) so
// neither (a) two distinct api-keys nor (b) a publisher named "operator"
// can cross-collide with another caller on the same Idempotency-Key.
const sqliteInsertMessageIdempotencySQL = `
INSERT INTO rimsky_message_idempotencies (
    instance_id, sender_kind, sender, sender_subject, idempotency_key, message_id, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (instance_id, sender_kind, sender, sender_subject, idempotency_key) DO NOTHING`

const sqliteSelectMessageIdempotencySQL = `
SELECT message_id, created_at
  FROM rimsky_message_idempotencies
 WHERE instance_id = ? AND sender_kind = ? AND sender = ? AND sender_subject = ? AND idempotency_key = ?`

func (b *messageIdempotenciesImpl) InsertOrLookup(ctx context.Context, tx persistence.Tx, row persistence.MessageIdempotencyRow) (persistence.MessageIdempotencyRow, bool, error) {
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now().UTC()
	}
	res, err := b.q(tx).ExecContext(ctx, sqliteInsertMessageIdempotencySQL,
		row.InstanceID.String(), row.SenderKind, row.Sender, row.SenderSubject, row.IdempotencyKey,
		row.MessageID.String(), row.CreatedAt.UTC().Format(time.RFC3339Nano))
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
	// Conflict — fetch the previously-recorded row.
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
	t, err := parseSQLiteTime(createdAtStr)
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
		cutoff.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("sqlite.MessageIdempotencies.DeleteOlderThan: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sqlite.MessageIdempotencies.DeleteOlderThan.RowsAffected: %w", err)
	}
	return n, nil
}

// parseSQLiteTime accepts the two formats SQLite produces for our
// TIMESTAMP columns: either RFC3339[Nano] (when written explicitly) or
// the `datetime('now')` default form ("2006-01-02 15:04:05").
func parseSQLiteTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02 15:04:05", s)
}
