// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type producerVerbOutboxImpl tablesImpl

var _ persistence.ProducerVerbOutboxTable = (*producerVerbOutboxImpl)(nil)

func (s *tablesImpl) ProducerVerbOutbox() persistence.ProducerVerbOutboxTable {
	return (*producerVerbOutboxImpl)(s)
}

func (b *producerVerbOutboxImpl) run(tx persistence.Tx) querier {
	if tx == nil {
		return (*tablesImpl)(b).db
	}
	return (*tablesImpl)(b).q(tx)
}

const sqliteInsertProducerVerbOutboxSQL = `
	INSERT OR IGNORE INTO rimsky_producer_verb_outbox
	    (claim_handle_id, producer_name, verb, claim_scope_data, address, lease_token,
	     supervisor_id, instance_id, parent_claim_handle_id, next_attempt_at, enqueued_at,
	     pending_lineage_record)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

func (b *producerVerbOutboxImpl) Enqueue(ctx context.Context, in persistence.ProducerVerbOutboxInsertInput, tx persistence.Tx) error {
	var instanceID any
	if in.InstanceID != nil {
		instanceID = in.InstanceID.String()
	}
	var parentID any
	if in.ParentClaimHandleID != nil {
		parentID = in.ParentClaimHandleID.String()
	}
	if _, err := b.run(tx).ExecContext(ctx, sqliteInsertProducerVerbOutboxSQL,
		in.ClaimHandleID.String(), in.ProducerName, string(in.Verb), in.ClaimScopeData, in.Address, in.LeaseToken,
		in.SupervisorID, instanceID, parentID,
		in.NextAttemptAt.UTC().Format(timeLayoutFixedNanos),
		in.EnqueuedAt.UTC().Format(timeLayoutFixedNanos),
		in.PendingLineageRecord,
	); err != nil {
		return fmt.Errorf("sqlite.ProducerVerbOutbox.Enqueue: %w", err)
	}
	return nil
}

const sqliteSelectProducerVerbOutboxSQL = `
	SELECT seq, claim_handle_id, producer_name, verb, claim_scope_data, address, lease_token,
	       supervisor_id, instance_id, parent_claim_handle_id, attempt_count, next_attempt_at, last_error, enqueued_at,
	       pending_lineage_record
	  FROM rimsky_producer_verb_outbox`

func (b *producerVerbOutboxImpl) ListAll(ctx context.Context, tx persistence.Tx) ([]persistence.ProducerVerbOutboxRow, error) {
	rows, err := b.run(tx).QueryContext(ctx, sqliteSelectProducerVerbOutboxSQL+` ORDER BY seq`)
	if err != nil {
		return nil, fmt.Errorf("sqlite.ProducerVerbOutbox.ListAll: %w", err)
	}
	defer rows.Close()
	return scanProducerVerbOutboxRows(rows)
}

func (b *producerVerbOutboxImpl) ListByProducer(ctx context.Context, producerName string, tx persistence.Tx) ([]persistence.ProducerVerbOutboxRow, error) {
	rows, err := b.run(tx).QueryContext(ctx, sqliteSelectProducerVerbOutboxSQL+` WHERE producer_name = ? ORDER BY seq`, producerName)
	if err != nil {
		return nil, fmt.Errorf("sqlite.ProducerVerbOutbox.ListByProducer: %w", err)
	}
	defer rows.Close()
	return scanProducerVerbOutboxRows(rows)
}

const sqliteRecordProducerVerbAttemptSQL = `
	UPDATE rimsky_producer_verb_outbox
	   SET attempt_count = attempt_count + 1, next_attempt_at = ?, last_error = ?
	 WHERE seq = ?`

func (b *producerVerbOutboxImpl) RecordAttempt(ctx context.Context, seq int64, nextAttemptAt time.Time, lastError string, tx persistence.Tx) error {
	if _, err := b.run(tx).ExecContext(ctx, sqliteRecordProducerVerbAttemptSQL,
		nextAttemptAt.UTC().Format(timeLayoutFixedNanos), lastError, seq,
	); err != nil {
		return fmt.Errorf("sqlite.ProducerVerbOutbox.RecordAttempt: %w", err)
	}
	return nil
}

func (b *producerVerbOutboxImpl) Delete(ctx context.Context, seq int64, tx persistence.Tx) error {
	if _, err := b.run(tx).ExecContext(ctx, `DELETE FROM rimsky_producer_verb_outbox WHERE seq = ?`, seq); err != nil {
		return fmt.Errorf("sqlite.ProducerVerbOutbox.Delete: %w", err)
	}
	return nil
}

func (b *producerVerbOutboxImpl) CountByProducer(ctx context.Context, tx persistence.Tx) (map[string]int, error) {
	rows, err := b.run(tx).QueryContext(ctx,
		`SELECT producer_name, COUNT(*) FROM rimsky_producer_verb_outbox GROUP BY producer_name`)
	if err != nil {
		return nil, fmt.Errorf("sqlite.ProducerVerbOutbox.CountByProducer: %w", err)
	}
	defer rows.Close()
	out := make(map[string]int)
	for rows.Next() {
		var name string
		var n int
		if err := rows.Scan(&name, &n); err != nil {
			return nil, fmt.Errorf("sqlite.ProducerVerbOutbox.CountByProducer.scan: %w", err)
		}
		out[name] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func scanProducerVerbOutboxRows(rows *sql.Rows) ([]persistence.ProducerVerbOutboxRow, error) {
	var out []persistence.ProducerVerbOutboxRow
	for rows.Next() {
		var (
			r             persistence.ProducerVerbOutboxRow
			handleStr     string
			verb          string
			instanceStr   sql.NullString
			parentStr     sql.NullString
			nextAttemptAt string
			enqueuedAt    string
		)
		if err := rows.Scan(&r.Seq, &handleStr, &r.ProducerName, &verb, &r.ClaimScopeData, &r.Address, &r.LeaseToken,
			&r.SupervisorID, &instanceStr, &parentStr, &r.AttemptCount, &nextAttemptAt, &r.LastError, &enqueuedAt,
			&r.PendingLineageRecord,
		); err != nil {
			return nil, fmt.Errorf("sqlite.ProducerVerbOutbox.scan: %w", err)
		}
		handle, err := uuid.Parse(handleStr)
		if err != nil {
			return nil, fmt.Errorf("sqlite.ProducerVerbOutbox.scan.claim_handle_id: %w", err)
		}
		r.ClaimHandleID = handle
		if instanceStr.Valid {
			inst, err := uuid.Parse(instanceStr.String)
			if err != nil {
				return nil, fmt.Errorf("sqlite.ProducerVerbOutbox.scan.instance_id: %w", err)
			}
			instID := shared.UUID(inst)
			r.InstanceID = &instID
		}
		if parentStr.Valid {
			parent, err := uuid.Parse(parentStr.String)
			if err != nil {
				return nil, fmt.Errorf("sqlite.ProducerVerbOutbox.scan.parent_claim_handle_id: %w", err)
			}
			parentID := shared.UUID(parent)
			r.ParentClaimHandleID = &parentID
		}
		if r.NextAttemptAt, err = parseTime(nextAttemptAt); err != nil {
			return nil, fmt.Errorf("sqlite.ProducerVerbOutbox.scan.next_attempt_at: %w", err)
		}
		if r.EnqueuedAt, err = parseTime(enqueuedAt); err != nil {
			return nil, fmt.Errorf("sqlite.ProducerVerbOutbox.scan.enqueued_at: %w", err)
		}
		r.Verb = persistence.ProducerVerb(verb)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
