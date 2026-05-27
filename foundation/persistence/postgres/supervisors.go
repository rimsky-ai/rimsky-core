// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// SupervisorTable — port of rimsky/src/storage/postgres/supervisor-store.ts.
// Adapted for rename: `accepts` TEXT[] → `accepted_executors` TEXT[];
// `active_cell_count` → `active_node_count`.
package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rimsky-ai/rimsky-core/foundation/persistence"
)

const supervisorCols = `
  id, accepted_executors, accepted_stores, concurrency, callback_host, callback_port,
  last_heartbeat_at, active_node_count, registered_at
`

func (s *supervisorsImpl) Register(ctx context.Context, in persistence.SupervisorRegisterInput, tx persistence.Tx) error {
	ex := s.q(tx)
	accepts := in.AcceptedExecutors
	if accepts == nil {
		accepts = []string{}
	}
	stores := in.AcceptedStores
	if stores == nil {
		stores = []string{}
	}
	_, err := ex.Exec(ctx,
		`INSERT INTO rimsky_supervisors
		   (id, accepted_executors, accepted_stores, concurrency, callback_host, callback_port, last_heartbeat_at)
		 VALUES ($1, $2, $3, $4, $5, $6, NOW())
		 ON CONFLICT (id) DO UPDATE
		   SET accepted_executors = EXCLUDED.accepted_executors,
		       accepted_stores    = EXCLUDED.accepted_stores,
		       concurrency        = EXCLUDED.concurrency,
		       callback_host      = EXCLUDED.callback_host,
		       callback_port      = EXCLUDED.callback_port,
		       last_heartbeat_at  = NOW(),
		       active_node_count  = 0`,
		in.ID, accepts, stores, in.Concurrency,
		nullableString(in.CallbackHost), nullableInt(in.CallbackPort),
	)
	return err
}

func (s *supervisorsImpl) Heartbeat(ctx context.Context, id string, activeNodeCount int, tx persistence.Tx) error {
	ex := s.q(tx)
	_, err := ex.Exec(ctx,
		`UPDATE rimsky_supervisors
		   SET last_heartbeat_at = NOW(),
		       active_node_count = $2
		 WHERE id = $1`,
		id, activeNodeCount,
	)
	return err
}

func (s *supervisorsImpl) Get(ctx context.Context, id string, tx persistence.Tx) (*persistence.SupervisorRow, error) {
	ex := s.q(tx)
	row := ex.QueryRow(ctx,
		`SELECT `+supervisorCols+` FROM rimsky_supervisors WHERE id = $1`, id)
	r, err := scanSupervisor(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

func (s *supervisorsImpl) List(ctx context.Context, tx persistence.Tx) ([]persistence.SupervisorRow, error) {
	ex := s.q(tx)
	rows, err := ex.Query(ctx,
		`SELECT `+supervisorCols+` FROM rimsky_supervisors
		 ORDER BY registered_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectSupervisors(rows)
}

func (s *supervisorsImpl) ListStale(ctx context.Context, cutoff time.Time, tx persistence.Tx) ([]persistence.SupervisorRow, error) {
	ex := s.q(tx)
	rows, err := ex.Query(ctx,
		`SELECT `+supervisorCols+` FROM rimsky_supervisors
		 WHERE last_heartbeat_at < $1`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectSupervisors(rows)
}

func (s *supervisorsImpl) Unregister(ctx context.Context, id string, tx persistence.Tx) error {
	ex := s.q(tx)
	_, err := ex.Exec(ctx, `DELETE FROM rimsky_supervisors WHERE id = $1`, id)
	return err
}

// ---- helpers ----

func scanSupervisor(sc scannable) (persistence.SupervisorRow, error) {
	var (
		r            persistence.SupervisorRow
		callbackHost *string
		callbackPort *int
	)
	if err := sc.Scan(
		&r.ID, &r.AcceptedExecutors, &r.AcceptedStores, &r.Concurrency,
		&callbackHost, &callbackPort,
		&r.LastHeartbeatAt, &r.ActiveNodeCount, &r.RegisteredAt,
	); err != nil {
		return persistence.SupervisorRow{}, err
	}
	r.CallbackHost = derefString(callbackHost)
	if callbackPort != nil {
		r.CallbackPort = *callbackPort
	}
	if r.AcceptedExecutors == nil {
		r.AcceptedExecutors = []string{}
	}
	if r.AcceptedStores == nil {
		r.AcceptedStores = []string{}
	}
	return r, nil
}

func collectSupervisors(rows pgx.Rows) ([]persistence.SupervisorRow, error) {
	var out []persistence.SupervisorRow
	for rows.Next() {
		r, err := scanSupervisor(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func nullableInt(i int) any {
	if i == 0 {
		return nil
	}
	return i
}
