// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: lifecycle-subscriber-at-least-once-delivery

package sqlite

import (
	"context"
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

const lifecycleOutboxCols = `seq, claim_producer_name, scope_kind, scope_id, event, payload, staged_at`

func (b *lifecycleOutboxImpl) Stage(ctx context.Context, in persistence.LifecycleOutboxRow, tx persistence.Tx) error {
	stagedAt := in.StagedAt
	if stagedAt.IsZero() {
		stagedAt = time.Now()
	}
	_, err := b.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_lifecycle_outbox (claim_producer_name, scope_kind, scope_id, event, payload, staged_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		in.ClaimProducerName, string(in.ScopeKind), in.ScopeID, in.Event, in.Payload, formatTime(stagedAt),
	)
	if err != nil {
		return fmt.Errorf("lifecycleoutbox.stage: %w", err)
	}
	return nil
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

func (b *lifecycleOutboxImpl) DeleteByScope(ctx context.Context, scopeKind persistence.LifecycleIdempotencyScopeKind, scopeID string, tx persistence.Tx) error {
	_, err := b.q(tx).ExecContext(ctx,
		`DELETE FROM rimsky_lifecycle_outbox WHERE scope_kind = ? AND scope_id = ?`,
		string(scopeKind), scopeID,
	)
	if err != nil {
		return fmt.Errorf("lifecycleoutbox.deleteByScope: %w", err)
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

func (b *lifecycleOutboxImpl) ListOldestPendingPerStream(ctx context.Context, limit int, tx persistence.Tx) ([]persistence.LifecycleOutboxRow, error) {
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
		 ORDER BY seq ASC
		 LIMIT ?`,
		limit,
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

func (b *lifecycleOutboxImpl) ListPendingForScope(ctx context.Context, scopeKind persistence.LifecycleIdempotencyScopeKind, scopeID string, tx persistence.Tx) ([]persistence.LifecycleOutboxRow, error) {
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

func scanLifecycleOutbox(sc scannable) (persistence.LifecycleOutboxRow, error) {
	var (
		seq               int64
		claimProducerName string
		scopeKindStr      string
		scopeID           string
		event             string
		payload           []byte
		stagedAtStr       string
	)
	if err := sc.Scan(&seq, &claimProducerName, &scopeKindStr, &scopeID, &event, &payload, &stagedAtStr); err != nil {
		return persistence.LifecycleOutboxRow{}, err
	}
	stagedAt, err := parseTime(stagedAtStr)
	if err != nil {
		return persistence.LifecycleOutboxRow{}, err
	}
	return persistence.LifecycleOutboxRow{
		Seq:               seq,
		ClaimProducerName: claimProducerName,
		ScopeKind:         persistence.LifecycleIdempotencyScopeKind(scopeKindStr),
		ScopeID:           scopeID,
		Event:             event,
		Payload:           payload,
		StagedAt:          stagedAt,
	}, nil
}
