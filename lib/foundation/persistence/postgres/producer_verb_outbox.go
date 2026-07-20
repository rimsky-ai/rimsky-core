// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

type producerVerbOutboxImpl tablesImpl

var _ persistence.ProducerVerbOutboxTable = (*producerVerbOutboxImpl)(nil)

func (s *tablesImpl) ProducerVerbOutbox() persistence.ProducerVerbOutboxTable {
	return (*producerVerbOutboxImpl)(s)
}

func (b *producerVerbOutboxImpl) run(tx persistence.Tx) querier {
	if tx == nil {
		return (*tablesImpl)(b).pool
	}
	return (*tablesImpl)(b).q(tx)
}

const insertProducerVerbOutboxSQL = `
	INSERT INTO rimsky_producer_verb_outbox
	    (claim_handle_id, producer_name, verb, claim_scope_data, address, lease_token,
	     supervisor_id, instance_id, parent_claim_handle_id, next_attempt_at, enqueued_at)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	ON CONFLICT (claim_handle_id, verb) DO NOTHING`

func (b *producerVerbOutboxImpl) Enqueue(ctx context.Context, in persistence.ProducerVerbOutboxInsertInput, tx persistence.Tx) error {
	if _, err := b.run(tx).Exec(ctx, insertProducerVerbOutboxSQL,
		in.ClaimHandleID, in.ProducerName, string(in.Verb), in.ClaimScopeData, in.Address, in.LeaseToken,
		in.SupervisorID, in.InstanceID, in.ParentClaimHandleID, in.NextAttemptAt, in.EnqueuedAt,
	); err != nil {
		return fmt.Errorf("postgres.ProducerVerbOutbox.Enqueue: %w", err)
	}
	return nil
}

const selectProducerVerbOutboxSQL = `
	SELECT seq, claim_handle_id, producer_name, verb, claim_scope_data, address, lease_token,
	       supervisor_id, instance_id, parent_claim_handle_id, attempt_count, next_attempt_at, last_error, enqueued_at
	  FROM rimsky_producer_verb_outbox`

func (b *producerVerbOutboxImpl) ListAll(ctx context.Context, tx persistence.Tx) ([]persistence.ProducerVerbOutboxRow, error) {
	rows, err := b.run(tx).Query(ctx, selectProducerVerbOutboxSQL+` ORDER BY seq`)
	if err != nil {
		return nil, fmt.Errorf("postgres.ProducerVerbOutbox.ListAll: %w", err)
	}
	defer rows.Close()
	return scanProducerVerbOutboxRows(rows)
}

func (b *producerVerbOutboxImpl) ListByProducer(ctx context.Context, producerName string, tx persistence.Tx) ([]persistence.ProducerVerbOutboxRow, error) {
	rows, err := b.run(tx).Query(ctx, selectProducerVerbOutboxSQL+` WHERE producer_name = $1 ORDER BY seq`, producerName)
	if err != nil {
		return nil, fmt.Errorf("postgres.ProducerVerbOutbox.ListByProducer: %w", err)
	}
	defer rows.Close()
	return scanProducerVerbOutboxRows(rows)
}

const recordProducerVerbAttemptSQL = `
	UPDATE rimsky_producer_verb_outbox
	   SET attempt_count = attempt_count + 1, next_attempt_at = $2, last_error = $3
	 WHERE seq = $1`

func (b *producerVerbOutboxImpl) RecordAttempt(ctx context.Context, seq int64, nextAttemptAt time.Time, lastError string, tx persistence.Tx) error {
	if _, err := b.run(tx).Exec(ctx, recordProducerVerbAttemptSQL, seq, nextAttemptAt, lastError); err != nil {
		return fmt.Errorf("postgres.ProducerVerbOutbox.RecordAttempt: %w", err)
	}
	return nil
}

func (b *producerVerbOutboxImpl) Delete(ctx context.Context, seq int64, tx persistence.Tx) error {
	if _, err := b.run(tx).Exec(ctx, `DELETE FROM rimsky_producer_verb_outbox WHERE seq = $1`, seq); err != nil {
		return fmt.Errorf("postgres.ProducerVerbOutbox.Delete: %w", err)
	}
	return nil
}

func (b *producerVerbOutboxImpl) CountByProducer(ctx context.Context, tx persistence.Tx) (map[string]int, error) {
	rows, err := b.run(tx).Query(ctx,
		`SELECT producer_name, COUNT(*) FROM rimsky_producer_verb_outbox GROUP BY producer_name`)
	if err != nil {
		return nil, fmt.Errorf("postgres.ProducerVerbOutbox.CountByProducer: %w", err)
	}
	defer rows.Close()
	out := make(map[string]int)
	for rows.Next() {
		var name string
		var n int
		if err := rows.Scan(&name, &n); err != nil {
			return nil, fmt.Errorf("postgres.ProducerVerbOutbox.CountByProducer.scan: %w", err)
		}
		out[name] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func scanProducerVerbOutboxRows(rows pgx.Rows) ([]persistence.ProducerVerbOutboxRow, error) {
	var out []persistence.ProducerVerbOutboxRow
	for rows.Next() {
		var (
			r    persistence.ProducerVerbOutboxRow
			verb string
		)
		if err := rows.Scan(&r.Seq, &r.ClaimHandleID, &r.ProducerName, &verb, &r.ClaimScopeData, &r.Address, &r.LeaseToken,
			&r.SupervisorID, &r.InstanceID, &r.ParentClaimHandleID, &r.AttemptCount, &r.NextAttemptAt, &r.LastError, &r.EnqueuedAt,
		); err != nil {
			return nil, fmt.Errorf("postgres.ProducerVerbOutbox.scan: %w", err)
		}
		r.Verb = persistence.ProducerVerb(verb)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
