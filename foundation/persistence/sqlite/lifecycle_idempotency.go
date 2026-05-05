// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// store_lifecycle.go — SQLite-backed persistence.LifecycleIdempotencyStore.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/fallguy/rimsky/foundation/persistence"
)

const storeLifecycleCols = `store_registration_name, scope_kind, scope_id, state, last_event_at`

func (s *lifecycleIdempotencyImpl) Get(ctx context.Context, storeName string, scopeKind persistence.LifecycleIdempotencyScopeKind, scopeID string, tx persistence.Tx) (*persistence.LifecycleIdempotencyRow, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT `+storeLifecycleCols+`
		 FROM rimsky_lifecycle_idempotency
		 WHERE store_registration_name = ? AND scope_kind = ? AND scope_id = ?`,
		storeName, string(scopeKind), scopeID,
	)
	r, err := scanLifecycleIdempotency(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("store_lifecycle.get: %w", err)
	}
	return &r, nil
}

func (s *lifecycleIdempotencyImpl) Upsert(ctx context.Context, in persistence.LifecycleIdempotencyRow, tx persistence.Tx) error {
	_, err := s.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_lifecycle_idempotency (store_registration_name, scope_kind, scope_id, state, last_event_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(store_registration_name, scope_kind, scope_id)
		 DO UPDATE SET state = excluded.state, last_event_at = excluded.last_event_at`,
		in.StoreRegistrationName, string(in.ScopeKind), in.ScopeID, string(in.State), nowUTC(),
	)
	if err != nil {
		return fmt.Errorf("store_lifecycle.upsert: %w", err)
	}
	return nil
}

func (s *lifecycleIdempotencyImpl) Delete(ctx context.Context, storeName string, scopeKind persistence.LifecycleIdempotencyScopeKind, scopeID string, tx persistence.Tx) error {
	_, err := s.q(tx).ExecContext(ctx,
		`DELETE FROM rimsky_lifecycle_idempotency
		 WHERE store_registration_name = ? AND scope_kind = ? AND scope_id = ?`,
		storeName, string(scopeKind), scopeID,
	)
	if err != nil {
		return fmt.Errorf("store_lifecycle.delete: %w", err)
	}
	return nil
}

func (s *lifecycleIdempotencyImpl) DeleteByScope(ctx context.Context, scopeKind persistence.LifecycleIdempotencyScopeKind, scopeID string, tx persistence.Tx) error {
	_, err := s.q(tx).ExecContext(ctx,
		`DELETE FROM rimsky_lifecycle_idempotency
		 WHERE scope_kind = ? AND scope_id = ?`,
		string(scopeKind), scopeID,
	)
	if err != nil {
		return fmt.Errorf("store_lifecycle.deleteByScope: %w", err)
	}
	return nil
}

func (s *lifecycleIdempotencyImpl) ListByScope(ctx context.Context, scopeKind persistence.LifecycleIdempotencyScopeKind, scopeID string, tx persistence.Tx) ([]persistence.LifecycleIdempotencyRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+storeLifecycleCols+`
		 FROM rimsky_lifecycle_idempotency
		 WHERE scope_kind = ? AND scope_id = ?
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

// ListByStore returns every lifecycle row for a given store
// registration name, regardless of scope. Used by the observability
// per-store detail endpoint.
func (s *lifecycleIdempotencyImpl) ListByStore(ctx context.Context, storeName string, tx persistence.Tx) ([]persistence.LifecycleIdempotencyRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+storeLifecycleCols+`
		 FROM rimsky_lifecycle_idempotency
		 WHERE store_registration_name = ?
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
		storeName      string
		scopeKindStr   string
		scopeID        string
		stateStr       string
		lastEventAtStr string
	)
	if err := sc.Scan(&storeName, &scopeKindStr, &scopeID, &stateStr, &lastEventAtStr); err != nil {
		return persistence.LifecycleIdempotencyRow{}, err
	}
	lastEventAt, err := parseTime(lastEventAtStr)
	if err != nil {
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
