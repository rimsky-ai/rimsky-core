// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

const lifecycleIdempotencyCols = `claim_producer_name, scope_kind, scope_id, state, last_event_at`

func (s *lifecycleIdempotencyImpl) Get(ctx context.Context, claimProducerName string, scopeKind persistence.LifecycleIdempotencyScopeKind, scopeID string, tx persistence.Tx) (*persistence.LifecycleIdempotencyRow, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT `+lifecycleIdempotencyCols+`
		 FROM rimsky_lifecycle_idempotencies
		 WHERE claim_producer_name = ? AND scope_kind = ? AND scope_id = ?`,
		claimProducerName, string(scopeKind), scopeID,
	)
	r, err := scanLifecycleIdempotency(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("lifecycleidempotency.get: %w", err)
	}
	return &r, nil
}

func (s *lifecycleIdempotencyImpl) Upsert(ctx context.Context, in persistence.LifecycleIdempotencyRow, tx persistence.Tx) error {
	lastEventAt := in.LastEventAt
	if lastEventAt.IsZero() {
		lastEventAt = time.Now()
	}
	_, err := s.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_lifecycle_idempotencies (claim_producer_name, scope_kind, scope_id, state, last_event_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(claim_producer_name, scope_kind, scope_id)
		 DO UPDATE SET state = excluded.state, last_event_at = excluded.last_event_at`,
		in.ClaimProducerName, string(in.ScopeKind), in.ScopeID, string(in.State), formatTime(lastEventAt),
	)
	if err != nil {
		return fmt.Errorf("lifecycleidempotency.upsert: %w", err)
	}
	return nil
}

func (s *lifecycleIdempotencyImpl) Delete(ctx context.Context, claimProducerName string, scopeKind persistence.LifecycleIdempotencyScopeKind, scopeID string, tx persistence.Tx) error {
	_, err := s.q(tx).ExecContext(ctx,
		`DELETE FROM rimsky_lifecycle_idempotencies
		 WHERE claim_producer_name = ? AND scope_kind = ? AND scope_id = ?`,
		claimProducerName, string(scopeKind), scopeID,
	)
	if err != nil {
		return fmt.Errorf("lifecycleidempotency.delete: %w", err)
	}
	return nil
}

func (s *lifecycleIdempotencyImpl) DeleteByScope(ctx context.Context, scopeKind persistence.LifecycleIdempotencyScopeKind, scopeID string, tx persistence.Tx) error {
	_, err := s.q(tx).ExecContext(ctx,
		`DELETE FROM rimsky_lifecycle_idempotencies
		 WHERE scope_kind = ? AND scope_id = ?`,
		string(scopeKind), scopeID,
	)
	if err != nil {
		return fmt.Errorf("lifecycleidempotency.deleteByScope: %w", err)
	}
	return nil
}

func (s *lifecycleIdempotencyImpl) ListByScope(ctx context.Context, scopeKind persistence.LifecycleIdempotencyScopeKind, scopeID string, tx persistence.Tx) ([]persistence.LifecycleIdempotencyRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+lifecycleIdempotencyCols+`
		 FROM rimsky_lifecycle_idempotencies
		 WHERE scope_kind = ? AND scope_id = ?
		 ORDER BY claim_producer_name ASC`,
		string(scopeKind), scopeID,
	)
	if err != nil {
		return nil, fmt.Errorf("lifecycleidempotency.listByScope: %w", err)
	}
	defer rows.Close()

	var out []persistence.LifecycleIdempotencyRow
	for rows.Next() {
		r, err := scanLifecycleIdempotency(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *lifecycleIdempotencyImpl) ListByClaimProducer(ctx context.Context, claimProducerName string, tx persistence.Tx) ([]persistence.LifecycleIdempotencyRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+lifecycleIdempotencyCols+`
		 FROM rimsky_lifecycle_idempotencies
		 WHERE claim_producer_name = ?
		 ORDER BY scope_kind ASC, scope_id ASC`,
		claimProducerName,
	)
	if err != nil {
		return nil, fmt.Errorf("lifecycleidempotency.listByClaimProducer: %w", err)
	}
	defer rows.Close()

	var out []persistence.LifecycleIdempotencyRow
	for rows.Next() {
		r, err := scanLifecycleIdempotency(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanLifecycleIdempotency(sc scannable) (persistence.LifecycleIdempotencyRow, error) {
	var (
		claimProducerName string
		scopeKindStr      string
		scopeID           string
		stateStr          string
		lastEventAtStr    string
	)
	if err := sc.Scan(&claimProducerName, &scopeKindStr, &scopeID, &stateStr, &lastEventAtStr); err != nil {
		return persistence.LifecycleIdempotencyRow{}, err
	}
	lastEventAt, err := parseTime(lastEventAtStr)
	if err != nil {
		return persistence.LifecycleIdempotencyRow{}, err
	}
	return persistence.LifecycleIdempotencyRow{
		ClaimProducerName: claimProducerName,
		ScopeKind:         persistence.LifecycleIdempotencyScopeKind(scopeKindStr),
		ScopeID:           scopeID,
		State:             persistence.LifecycleIdempotencyState(stateStr),
		LastEventAt:       lastEventAt,
	}, nil
}
