// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: lifecycle-subscriber-at-least-once-delivery

package postgres

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
	_, err := b.q(tx).Exec(ctx,
		`INSERT INTO rimsky_lifecycle_outbox (claim_producer_name, scope_kind, scope_id, event, payload, staged_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		in.ClaimProducerName, string(in.ScopeKind), in.ScopeID, in.Event, in.Payload, stagedAt,
	)
	if err != nil {
		return fmt.Errorf("lifecycleoutbox.stage: %w", err)
	}
	return nil
}

func (b *lifecycleOutboxImpl) DeleteBySeq(ctx context.Context, seq int64, tx persistence.Tx) error {
	_, err := b.q(tx).Exec(ctx,
		`DELETE FROM rimsky_lifecycle_outbox WHERE seq = $1`, seq,
	)
	if err != nil {
		return fmt.Errorf("lifecycleoutbox.deleteBySeq: %w", err)
	}
	return nil
}

func (b *lifecycleOutboxImpl) DeleteByScope(ctx context.Context, scopeKind persistence.LifecycleIdempotencyScopeKind, scopeID string, tx persistence.Tx) error {
	_, err := b.q(tx).Exec(ctx,
		`DELETE FROM rimsky_lifecycle_outbox WHERE scope_kind = $1 AND scope_id = $2`,
		string(scopeKind), scopeID,
	)
	if err != nil {
		return fmt.Errorf("lifecycleoutbox.deleteByScope: %w", err)
	}
	return nil
}

// @decision: lifecycle-subscriber-at-least-once-delivery
func (b *lifecycleOutboxImpl) DeleteOlderThan(ctx context.Context, cutoff time.Time, tx persistence.Tx) (int64, error) {
	tag, err := b.q(tx).Exec(ctx,
		`DELETE FROM rimsky_lifecycle_outbox WHERE staged_at < $1`, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("lifecycleoutbox.deleteOlderThan: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (b *lifecycleOutboxImpl) ListOldestPendingPerStream(ctx context.Context, limit int, tx persistence.Tx) ([]persistence.LifecycleOutboxRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := b.q(tx).Query(ctx,
		`SELECT `+lifecycleOutboxCols+` FROM (
		     SELECT DISTINCT ON (claim_producer_name, scope_kind, scope_id) `+lifecycleOutboxCols+`
		       FROM rimsky_lifecycle_outbox
		      ORDER BY claim_producer_name, scope_kind, scope_id, seq ASC
		 ) heads
		 ORDER BY seq ASC
		 LIMIT $1`,
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
	rows, err := b.q(tx).Query(ctx,
		`SELECT `+lifecycleOutboxCols+`
		 FROM rimsky_lifecycle_outbox
		 WHERE scope_kind = $1 AND scope_id = $2
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
		stagedAt          time.Time
	)
	if err := sc.Scan(&seq, &claimProducerName, &scopeKindStr, &scopeID, &event, &payload, &stagedAt); err != nil {
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
