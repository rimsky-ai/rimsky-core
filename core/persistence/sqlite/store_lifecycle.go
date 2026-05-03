// store_lifecycle.go — SQLite-backed persistence.StoreLifecycleStore.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/fallguy/rimsky/core/persistence"
)

const storeLifecycleCols = `store_registration_name, scope_kind, scope_id, state, last_event_at`

func (s *storeLifecycleImpl) Get(ctx context.Context, storeName string, scopeKind persistence.StoreLifecycleScopeKind, scopeID string, tx persistence.Tx) (*persistence.StoreLifecycleRow, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT `+storeLifecycleCols+`
		 FROM rimsky_store_lifecycle
		 WHERE store_registration_name = ? AND scope_kind = ? AND scope_id = ?`,
		storeName, string(scopeKind), scopeID,
	)
	r, err := scanStoreLifecycle(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("store_lifecycle.get: %w", err)
	}
	return &r, nil
}

func (s *storeLifecycleImpl) Upsert(ctx context.Context, in persistence.StoreLifecycleRow, tx persistence.Tx) error {
	_, err := s.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_store_lifecycle (store_registration_name, scope_kind, scope_id, state, last_event_at)
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

func (s *storeLifecycleImpl) Delete(ctx context.Context, storeName string, scopeKind persistence.StoreLifecycleScopeKind, scopeID string, tx persistence.Tx) error {
	_, err := s.q(tx).ExecContext(ctx,
		`DELETE FROM rimsky_store_lifecycle
		 WHERE store_registration_name = ? AND scope_kind = ? AND scope_id = ?`,
		storeName, string(scopeKind), scopeID,
	)
	if err != nil {
		return fmt.Errorf("store_lifecycle.delete: %w", err)
	}
	return nil
}

func (s *storeLifecycleImpl) DeleteByScope(ctx context.Context, scopeKind persistence.StoreLifecycleScopeKind, scopeID string, tx persistence.Tx) error {
	_, err := s.q(tx).ExecContext(ctx,
		`DELETE FROM rimsky_store_lifecycle
		 WHERE scope_kind = ? AND scope_id = ?`,
		string(scopeKind), scopeID,
	)
	if err != nil {
		return fmt.Errorf("store_lifecycle.deleteByScope: %w", err)
	}
	return nil
}

func (s *storeLifecycleImpl) ListByScope(ctx context.Context, scopeKind persistence.StoreLifecycleScopeKind, scopeID string, tx persistence.Tx) ([]persistence.StoreLifecycleRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+storeLifecycleCols+`
		 FROM rimsky_store_lifecycle
		 WHERE scope_kind = ? AND scope_id = ?
		 ORDER BY store_registration_name ASC`,
		string(scopeKind), scopeID,
	)
	if err != nil {
		return nil, fmt.Errorf("store_lifecycle.listByScope: %w", err)
	}
	defer rows.Close()

	var out []persistence.StoreLifecycleRow
	for rows.Next() {
		r, err := scanStoreLifecycle(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanStoreLifecycle(sc scannable) (persistence.StoreLifecycleRow, error) {
	var (
		storeName      string
		scopeKindStr   string
		scopeID        string
		stateStr       string
		lastEventAtStr string
	)
	if err := sc.Scan(&storeName, &scopeKindStr, &scopeID, &stateStr, &lastEventAtStr); err != nil {
		return persistence.StoreLifecycleRow{}, err
	}
	lastEventAt, err := parseTime(lastEventAtStr)
	if err != nil {
		return persistence.StoreLifecycleRow{}, err
	}
	return persistence.StoreLifecycleRow{
		StoreRegistrationName: storeName,
		ScopeKind:             persistence.StoreLifecycleScopeKind(scopeKindStr),
		ScopeID:               scopeID,
		State:                 persistence.StoreLifecycleState(stateStr),
		LastEventAt:           lastEventAt,
	}, nil
}
