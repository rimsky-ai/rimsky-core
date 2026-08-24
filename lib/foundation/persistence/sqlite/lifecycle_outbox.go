// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: lifecycle-subscriber-at-least-once-delivery

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

type lifecycleOutboxImpl tablesImpl

var _ persistence.LifecycleOutboxTable = (*lifecycleOutboxImpl)(nil)

func (s *tablesImpl) LifecycleOutbox() persistence.LifecycleOutboxTable {
	return (*lifecycleOutboxImpl)(s)
}

func (b *lifecycleOutboxImpl) q(tx persistence.Tx) querier { return (*tablesImpl)(b).q(tx) }

const lifecycleOutboxCols = `seq, claim_producer_name, scope_kind, scope_id, event, payload, staged_at, attempt_count, next_attempt_at, last_error`

func (b *lifecycleOutboxImpl) Stage(ctx context.Context, in persistence.LifecycleOutboxRow, tx persistence.Tx) error {
	stagedAt := in.StagedAt
	if stagedAt.IsZero() {
		stagedAt = time.Now()
	}
	nextAttemptAt := in.NextAttemptAt
	if nextAttemptAt.IsZero() {
		nextAttemptAt = stagedAt
	}
	_, err := b.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_lifecycle_outbox (claim_producer_name, scope_kind, scope_id, event, payload, staged_at, next_attempt_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		in.ClaimProducerName, string(in.ScopeKind), in.ScopeID, in.Event, in.Payload,
		formatTime(stagedAt), formatTime(nextAttemptAt),
	)
	if err != nil {
		return fmt.Errorf("lifecycleoutbox.stage: %w", err)
	}
	return nil
}

// @decision: lifecycle-subscriber-at-least-once-delivery
func (b *lifecycleOutboxImpl) GetBySeq(ctx context.Context, seq int64, tx persistence.Tx) (*persistence.LifecycleOutboxRow, error) {
	row, err := scanLifecycleOutbox(b.q(tx).QueryRowContext(ctx,
		`SELECT `+lifecycleOutboxCols+` FROM rimsky_lifecycle_outbox WHERE seq = ?`, seq))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("lifecycleoutbox.getBySeq: %w", err)
	}
	return &row, nil
}

func (b *lifecycleOutboxImpl) DeleteBySeq(ctx context.Context, seq int64, tx persistence.Tx) error {
	_, err := b.q(tx).ExecContext(ctx,
		`DELETE FROM rimsky_lifecycle_outbox WHERE seq = ?`, seq,
	)
	if err != nil {
		return fmt.Errorf("lifecycleoutbox.deleteBySeq: %w", err)
	}
	return nil
}

// @decision: lifecycle-subscriber-at-least-once-delivery
func (b *lifecycleOutboxImpl) DeleteOlderThan(ctx context.Context, cutoff time.Time, tx persistence.Tx) (int64, error) {
	res, err := b.q(tx).ExecContext(ctx,
		`DELETE FROM rimsky_lifecycle_outbox WHERE staged_at < ?`, formatTime(cutoff),
	)
	if err != nil {
		return 0, fmt.Errorf("lifecycleoutbox.deleteOlderThan: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("lifecycleoutbox.deleteOlderThan: rows affected: %w", err)
	}
	return n, nil
}

// @decision: service-delivery-stall-signal
func (b *lifecycleOutboxImpl) RecordAttempt(ctx context.Context, seq int64, nextAttemptAt time.Time, lastError string, tx persistence.Tx) error {
	_, err := b.q(tx).ExecContext(ctx,
		`UPDATE rimsky_lifecycle_outbox
		    SET attempt_count = attempt_count + 1, next_attempt_at = ?, last_error = ?
		  WHERE seq = ?`,
		formatTime(nextAttemptAt), lastError, seq,
	)
	if err != nil {
		return fmt.Errorf("lifecycleoutbox.recordAttempt: %w", err)
	}
	return nil
}

// @decision: lifecycle-subscriber-at-least-once-delivery
func (b *lifecycleOutboxImpl) ListOldestPendingPerStream(ctx context.Context, limit int, dueAt time.Time, tx persistence.Tx) ([]persistence.LifecycleOutboxRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := b.q(tx).QueryContext(ctx,
		`SELECT `+lifecycleOutboxCols+`
		 FROM rimsky_lifecycle_outbox
		 WHERE seq IN (
		     SELECT MIN(seq) FROM rimsky_lifecycle_outbox
		      GROUP BY claim_producer_name, scope_kind, scope_id
		 )
		   AND next_attempt_at <= ?
		 ORDER BY seq ASC
		 LIMIT ?`,
		formatTime(dueAt), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("lifecycleoutbox.listOldestPendingPerStream: %w", err)
	}
	defer rows.Close()

	var out []persistence.LifecycleOutboxRow
	for rows.Next() {
		r, err := scanLifecycleOutbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (b *lifecycleOutboxImpl) ListPendingForScope(ctx context.Context, scopeKind persistence.LifecycleScopeKind, scopeID string, tx persistence.Tx) ([]persistence.LifecycleOutboxRow, error) {
	rows, err := b.q(tx).QueryContext(ctx,
		`SELECT `+lifecycleOutboxCols+`
		 FROM rimsky_lifecycle_outbox
		 WHERE scope_kind = ? AND scope_id = ?
		 ORDER BY seq ASC`,
		string(scopeKind), scopeID,
	)
	if err != nil {
		return nil, fmt.Errorf("lifecycleoutbox.listPendingForScope: %w", err)
	}
	defer rows.Close()

	var out []persistence.LifecycleOutboxRow
	for rows.Next() {
		r, err := scanLifecycleOutbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// @decision: service-delivery-stall-signal
func (b *lifecycleOutboxImpl) ListPendingForService(ctx context.Context, claimProducerName string, limit int, tx persistence.Tx) ([]persistence.LifecycleOutboxRow, error) {
	if limit <= 0 {
		limit = persistence.DefaultServiceOutboxPageSize
	}
	rows, err := b.q(tx).QueryContext(ctx,
		`SELECT `+lifecycleOutboxCols+`
		 FROM rimsky_lifecycle_outbox
		 WHERE claim_producer_name = ?
		 ORDER BY seq ASC
		 LIMIT ?`,
		claimProducerName, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("lifecycleoutbox.listPendingForService: %w", err)
	}
	defer rows.Close()

	out := []persistence.LifecycleOutboxRow{}
	for rows.Next() {
		r, err := scanLifecycleOutbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// @decision: service-delivery-stall-signal
func (b *lifecycleOutboxImpl) PendingSummaryByService(ctx context.Context, tx persistence.Tx) ([]persistence.ServiceOutboxPending, error) {
	rows, err := b.q(tx).QueryContext(ctx,
		`SELECT claim_producer_name, COUNT(*), MIN(staged_at)
		   FROM rimsky_lifecycle_outbox
		  GROUP BY claim_producer_name
		  ORDER BY claim_producer_name ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("lifecycleoutbox.pendingSummaryByService: %w", err)
	}
	defer rows.Close()

	out := []persistence.ServiceOutboxPending{}
	for rows.Next() {
		var (
			e           persistence.ServiceOutboxPending
			oldestAtStr string
		)
		if err := rows.Scan(&e.Service, &e.PendingCount, &oldestAtStr); err != nil {
			return nil, fmt.Errorf("lifecycleoutbox.pendingSummaryByService: %w", err)
		}
		oldestAt, err := parseTime(oldestAtStr)
		if err != nil {
			return nil, fmt.Errorf("lifecycleoutbox.pendingSummaryByService: %w", err)
		}
		e.OldestPendingAt = oldestAt
		out = append(out, e)
	}
	return out, rows.Err()
}

func scanLifecycleOutbox(sc scannable) (persistence.LifecycleOutboxRow, error) {
	var (
		seq               int64
		claimProducerName string
		scopeKindStr      string
		scopeID           string
		event             string
		payload           []byte
		stagedAtStr       string
		attemptCount      int
		nextAttemptAtStr  string
		lastError         string
	)
	if err := sc.Scan(&seq, &claimProducerName, &scopeKindStr, &scopeID, &event, &payload, &stagedAtStr,
		&attemptCount, &nextAttemptAtStr, &lastError); err != nil {
		return persistence.LifecycleOutboxRow{}, err
	}
	stagedAt, err := parseTime(stagedAtStr)
	if err != nil {
		return persistence.LifecycleOutboxRow{}, err
	}
	nextAttemptAt, err := parseTime(nextAttemptAtStr)
	if err != nil {
		return persistence.LifecycleOutboxRow{}, err
	}
	return persistence.LifecycleOutboxRow{
		Seq:               seq,
		ClaimProducerName: claimProducerName,
		ScopeKind:         persistence.LifecycleScopeKind(scopeKindStr),
		ScopeID:           scopeID,
		Event:             event,
		Payload:           payload,
		StagedAt:          stagedAt,
		AttemptCount:      attemptCount,
		NextAttemptAt:     nextAttemptAt,
		LastError:         lastError,
	}, nil
}
