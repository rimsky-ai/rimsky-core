// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

type messageIdempotenciesImpl tablesImpl

var _ persistence.MessageIdempotencyTable = (*messageIdempotenciesImpl)(nil)

func (s *tablesImpl) MessageIdempotencies() persistence.MessageIdempotencyTable {
	return (*messageIdempotenciesImpl)(s)
}

func (b *messageIdempotenciesImpl) q(tx persistence.Tx) querier { return (*tablesImpl)(b).q(tx) }

const upsertMessageIdempotencySQL = `
INSERT INTO rimsky_message_idempotencies (
    instance_id, sender_kind, sender, sender_subject, idempotency_key, message_id, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (instance_id, sender_kind, sender, sender_subject, idempotency_key) DO UPDATE
   SET message_id = rimsky_message_idempotencies.message_id
RETURNING message_id, created_at, (xmax = 0) AS inserted`

func (b *messageIdempotenciesImpl) InsertOrLookup(ctx context.Context, row persistence.MessageIdempotencyRow, tx persistence.Tx) (persistence.MessageIdempotencyRow, bool, error) {
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now().UTC()
	}
	var outMessageID = row.MessageID
	var outCreatedAt = row.CreatedAt
	var inserted bool
	err := b.q(tx).QueryRow(ctx, upsertMessageIdempotencySQL,
		row.InstanceID, row.SenderKind, row.Sender, row.SenderSubject, row.IdempotencyKey, row.MessageID, row.CreatedAt,
	).Scan(&outMessageID, &outCreatedAt, &inserted)
	if err != nil {
		return persistence.MessageIdempotencyRow{}, false, fmt.Errorf("postgres.MessageIdempotencies.InsertOrLookup: %w", err)
	}
	return persistence.MessageIdempotencyRow{
		InstanceID:     row.InstanceID,
		SenderKind:     row.SenderKind,
		Sender:         row.Sender,
		SenderSubject:  row.SenderSubject,
		IdempotencyKey: row.IdempotencyKey,
		MessageID:      outMessageID,
		CreatedAt:      outCreatedAt,
	}, inserted, nil
}

const deleteMessageIdempotenciesOlderThanSQL = `
DELETE FROM rimsky_message_idempotencies WHERE created_at < $1`

func (b *messageIdempotenciesImpl) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := (*tablesImpl)(b).pool.Exec(ctx, deleteMessageIdempotenciesOlderThanSQL, cutoff)
	if err != nil {
		return 0, fmt.Errorf("postgres.MessageIdempotencies.DeleteOlderThan: %w", err)
	}
	return tag.RowsAffected(), nil
}
