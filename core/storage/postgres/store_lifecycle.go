// StoreLifecycleStore — Postgres-backed storage.StoreLifecycleStore.
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
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/storage"
)

type StoreLifecycleStore struct {
	pool *pgxpool.Pool
}

var _ storage.StoreLifecycleStore = (*StoreLifecycleStore)(nil)

const storeLifecycleCols = `store_registration_name, scope_kind, scope_id, state, last_event_at`

func (s *StoreLifecycleStore) Get(ctx context.Context, storeName string, scopeKind storage.StoreLifecycleScopeKind, scopeID string, tx storage.Tx) (*storage.StoreLifecycleRow, error) {
	ex := q(tx, s.pool)
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

func (s *StoreLifecycleStore) Upsert(ctx context.Context, in storage.StoreLifecycleRow, tx storage.Tx) error {
	ex := q(tx, s.pool)
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

func (s *StoreLifecycleStore) Delete(ctx context.Context, storeName string, scopeKind storage.StoreLifecycleScopeKind, scopeID string, tx storage.Tx) error {
	ex := q(tx, s.pool)
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

func (s *StoreLifecycleStore) DeleteByScope(ctx context.Context, scopeKind storage.StoreLifecycleScopeKind, scopeID string, tx storage.Tx) error {
	ex := q(tx, s.pool)
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

func (s *StoreLifecycleStore) ListByScope(ctx context.Context, scopeKind storage.StoreLifecycleScopeKind, scopeID string, tx storage.Tx) ([]storage.StoreLifecycleRow, error) {
	ex := q(tx, s.pool)
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

	var out []storage.StoreLifecycleRow
	for rows.Next() {
		r, err := scanStoreLifecycle(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanStoreLifecycle(sc scannable) (storage.StoreLifecycleRow, error) {
	var (
		storeName    string
		scopeKindStr string
		scopeID      string
		stateStr     string
		lastEventAt  time.Time
	)
	if err := sc.Scan(&storeName, &scopeKindStr, &scopeID, &stateStr, &lastEventAt); err != nil {
		return storage.StoreLifecycleRow{}, err
	}
	return storage.StoreLifecycleRow{
		StoreRegistrationName: storeName,
		ScopeKind:             storage.StoreLifecycleScopeKind(scopeKindStr),
		ScopeID:               scopeID,
		State:                 storage.StoreLifecycleState(stateStr),
		LastEventAt:           lastEventAt,
	}, nil
}
