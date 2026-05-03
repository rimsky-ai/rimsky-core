// StoreLifecycleStore — Postgres-backed persistence.StoreLifecycleStore.
// Per docs/specs/2026-05-01-control-plane-and-store-lifecycle-design.md
// §5.3: rimsky_store_lifecycle is the per-(store, scope) bookkeeping
// table that drives idempotent fan-out of lifecycle events.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/fallguy/rimsky/core/persistence"
)

const storeLifecycleCols = `store_registration_name, scope_kind, scope_id, state, last_event_at`

func (s *storeLifecycleImpl) Get(ctx context.Context, storeName string, scopeKind persistence.StoreLifecycleScopeKind, scopeID string, tx persistence.Tx) (*persistence.StoreLifecycleRow, error) {
	ex := s.q(tx)
	row := ex.QueryRow(ctx,
		`SELECT `+storeLifecycleCols+`
		 FROM rimsky_store_lifecycle
		 WHERE store_registration_name = $1 AND scope_kind = $2 AND scope_id = $3`,
		storeName, string(scopeKind), scopeID,
	)
	r, err := scanStoreLifecycle(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("store_lifecycle.get: %w", err)
	}
	return &r, nil
}

func (s *storeLifecycleImpl) Upsert(ctx context.Context, in persistence.StoreLifecycleRow, tx persistence.Tx) error {
	ex := s.q(tx)
	_, err := ex.Exec(ctx,
		`INSERT INTO rimsky_store_lifecycle (store_registration_name, scope_kind, scope_id, state, last_event_at)
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

func (s *storeLifecycleImpl) Delete(ctx context.Context, storeName string, scopeKind persistence.StoreLifecycleScopeKind, scopeID string, tx persistence.Tx) error {
	ex := s.q(tx)
	_, err := ex.Exec(ctx,
		`DELETE FROM rimsky_store_lifecycle
		 WHERE store_registration_name = $1 AND scope_kind = $2 AND scope_id = $3`,
		storeName, string(scopeKind), scopeID,
	)
	if err != nil {
		return fmt.Errorf("store_lifecycle.delete: %w", err)
	}
	return nil
}

func (s *storeLifecycleImpl) DeleteByScope(ctx context.Context, scopeKind persistence.StoreLifecycleScopeKind, scopeID string, tx persistence.Tx) error {
	ex := s.q(tx)
	_, err := ex.Exec(ctx,
		`DELETE FROM rimsky_store_lifecycle
		 WHERE scope_kind = $1 AND scope_id = $2`,
		string(scopeKind), scopeID,
	)
	if err != nil {
		return fmt.Errorf("store_lifecycle.deleteByScope: %w", err)
	}
	return nil
}

func (s *storeLifecycleImpl) ListByScope(ctx context.Context, scopeKind persistence.StoreLifecycleScopeKind, scopeID string, tx persistence.Tx) ([]persistence.StoreLifecycleRow, error) {
	ex := s.q(tx)
	rows, err := ex.Query(ctx,
		`SELECT `+storeLifecycleCols+`
		 FROM rimsky_store_lifecycle
		 WHERE scope_kind = $1 AND scope_id = $2
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
		storeName    string
		scopeKindStr string
		scopeID      string
		stateStr     string
		lastEventAt  time.Time
	)
	if err := sc.Scan(&storeName, &scopeKindStr, &scopeID, &stateStr, &lastEventAt); err != nil {
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
