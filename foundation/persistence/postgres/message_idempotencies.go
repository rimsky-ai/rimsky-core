// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Postgres impl of persistence.MessageIdempotencyTable — universal
// dedup-tuple table for POST /instances/{id}/messages.

package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/fallguyconsulting/rimsky/foundation/persistence"
)

type messageIdempotenciesImpl tablesImpl

var _ persistence.MessageIdempotencyTable = (*messageIdempotenciesImpl)(nil)

// MessageIdempotencies returns the postgres MessageIdempotencyTable impl.
func (s *tablesImpl) MessageIdempotencies() persistence.MessageIdempotencyTable {
	return (*messageIdempotenciesImpl)(s)
}

func (b *messageIdempotenciesImpl) q(tx persistence.Tx) querier { return (*tablesImpl)(b).q(tx) }

// upsertMessageIdempotencySQL atomically inserts the tuple or, on
// unique-key conflict, returns the previously-recorded message_id +
// created_at and inserted=false. The `xmax = 0` predicate is the
// postgres-idiomatic way to distinguish "fresh insert" from
// "conflict-replay" from a single RETURNING clause.
const upsertMessageIdempotencySQL = `
INSERT INTO rimsky_message_idempotencies (
    instance_id, sender, idempotency_key, message_id, created_at
) VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (instance_id, sender, idempotency_key) DO UPDATE
   SET message_id = rimsky_message_idempotencies.message_id
RETURNING message_id, created_at, (xmax = 0) AS inserted`

func (b *messageIdempotenciesImpl) InsertOrLookup(ctx context.Context, tx persistence.Tx, row persistence.MessageIdempotencyRow) (persistence.MessageIdempotencyRow, bool, error) {
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now().UTC()
	}
	var outMessageID = row.MessageID
	var outCreatedAt = row.CreatedAt
	var inserted bool
	err := b.q(tx).QueryRow(ctx, upsertMessageIdempotencySQL,
		row.InstanceID, row.Sender, row.IdempotencyKey, row.MessageID, row.CreatedAt,
	).Scan(&outMessageID, &outCreatedAt, &inserted)
	if err != nil {
		return persistence.MessageIdempotencyRow{}, false, fmt.Errorf("postgres.MessageIdempotencies.InsertOrLookup: %w", err)
	}
	return persistence.MessageIdempotencyRow{
		InstanceID:     row.InstanceID,
		Sender:         row.Sender,
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
