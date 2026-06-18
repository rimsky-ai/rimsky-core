// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

const storeLifecycleCols = `store_registration_name, scope_kind, scope_id, state, last_event_at`

func (s *lifecycleIdempotencyImpl) Get(ctx context.Context, storeName string, scopeKind persistence.LifecycleIdempotencyScopeKind, scopeID string, tx persistence.Tx) (*persistence.LifecycleIdempotencyRow, error) {
	ex := s.q(tx)
	row := ex.QueryRow(ctx,
		`SELECT `+storeLifecycleCols+`
		 FROM rimsky_lifecycle_idempotencies
		 WHERE store_registration_name = $1 AND scope_kind = $2 AND scope_id = $3`,
		storeName, string(scopeKind), scopeID,
	)
	r, err := scanLifecycleIdempotency(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("store_lifecycle.get: %w", err)
	}
	return &r, nil
}

func (s *lifecycleIdempotencyImpl) Upsert(ctx context.Context, in persistence.LifecycleIdempotencyRow, tx persistence.Tx) error {
	ex := s.q(tx)
	_, err := ex.Exec(ctx,
		`INSERT INTO rimsky_lifecycle_idempotencies (store_registration_name, scope_kind, scope_id, state, last_event_at)
		 VALUES ($1, $2, $3, $4, now())
		 ON CONFLICT (store_registration_name, scope_kind, scope_id)
		 DO UPDATE SET state = EXCLUDED.state, last_event_at = now()`,
		in.StoreRegistrationName, string(in.ScopeKind), in.ScopeID, string(in.State),
	)
	if err != nil {
		return fmt.Errorf("store_lifecycle.upsert: %w", err)
	}
	return nil
}

func (s *lifecycleIdempotencyImpl) Delete(ctx context.Context, storeName string, scopeKind persistence.LifecycleIdempotencyScopeKind, scopeID string, tx persistence.Tx) error {
	ex := s.q(tx)
	_, err := ex.Exec(ctx,
		`DELETE FROM rimsky_lifecycle_idempotencies
		 WHERE store_registration_name = $1 AND scope_kind = $2 AND scope_id = $3`,
		storeName, string(scopeKind), scopeID,
	)
	if err != nil {
		return fmt.Errorf("store_lifecycle.delete: %w", err)
	}
	return nil
}

func (s *lifecycleIdempotencyImpl) DeleteByScope(ctx context.Context, scopeKind persistence.LifecycleIdempotencyScopeKind, scopeID string, tx persistence.Tx) error {
	ex := s.q(tx)
	_, err := ex.Exec(ctx,
		`DELETE FROM rimsky_lifecycle_idempotencies
		 WHERE scope_kind = $1 AND scope_id = $2`,
		string(scopeKind), scopeID,
	)
	if err != nil {
		return fmt.Errorf("store_lifecycle.deleteByScope: %w", err)
	}
	return nil
}

func (s *lifecycleIdempotencyImpl) ListByScope(ctx context.Context, scopeKind persistence.LifecycleIdempotencyScopeKind, scopeID string, tx persistence.Tx) ([]persistence.LifecycleIdempotencyRow, error) {
	ex := s.q(tx)
	rows, err := ex.Query(ctx,
		`SELECT `+storeLifecycleCols+`
		 FROM rimsky_lifecycle_idempotencies
		 WHERE scope_kind = $1 AND scope_id = $2
		 ORDER BY store_registration_name ASC`,
		string(scopeKind), scopeID,
	)
	if err != nil {
		return nil, fmt.Errorf("store_lifecycle.listByScope: %w", err)
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

func (s *lifecycleIdempotencyImpl) ListByStore(ctx context.Context, storeName string, tx persistence.Tx) ([]persistence.LifecycleIdempotencyRow, error) {
	ex := s.q(tx)
	rows, err := ex.Query(ctx,
		`SELECT `+storeLifecycleCols+`
		 FROM rimsky_lifecycle_idempotencies
		 WHERE store_registration_name = $1
		 ORDER BY scope_kind ASC, scope_id ASC`,
		storeName,
	)
	if err != nil {
		return nil, fmt.Errorf("store_lifecycle.listByStore: %w", err)
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
		storeName    string
		scopeKindStr string
		scopeID      string
		stateStr     string
		lastEventAt  time.Time
	)
	if err := sc.Scan(&storeName, &scopeKindStr, &scopeID, &stateStr, &lastEventAt); err != nil {
		return persistence.LifecycleIdempotencyRow{}, err
	}
	return persistence.LifecycleIdempotencyRow{
		StoreRegistrationName: storeName,
		ScopeKind:             persistence.LifecycleIdempotencyScopeKind(scopeKindStr),
		ScopeID:               scopeID,
		State:                 persistence.LifecycleIdempotencyState(stateStr),
		LastEventAt:           lastEventAt,
	}, nil
}
