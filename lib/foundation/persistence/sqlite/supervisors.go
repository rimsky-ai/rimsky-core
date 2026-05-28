// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// supervisors.go — SQLite-backed persistence.SupervisorTable.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

const supervisorCols = `
  id, accepted_executors, accepted_stores, concurrency, callback_host, callback_port,
  last_heartbeat_at, active_node_count, registered_at
`

func (s *supervisorsImpl) Register(ctx context.Context, in persistence.SupervisorRegisterInput, tx persistence.Tx) error {
	accepts := in.AcceptedExecutors
	if accepts == nil {
		accepts = []string{}
	}
	stores := in.AcceptedStores
	if stores == nil {
		stores = []string{}
	}
	now := nowUTC()
	_, err := s.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_supervisors
		   (id, accepted_executors, accepted_stores, concurrency, callback_host, callback_port, last_heartbeat_at, registered_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE
		   SET accepted_executors = excluded.accepted_executors,
		       accepted_stores    = excluded.accepted_stores,
		       concurrency        = excluded.concurrency,
		       callback_host      = excluded.callback_host,
		       callback_port      = excluded.callback_port,
		       last_heartbeat_at  = excluded.last_heartbeat_at,
		       active_node_count  = 0`,
		in.ID, marshalStringArray(accepts), marshalStringArray(stores), in.Concurrency,
		nullableString(in.CallbackHost), nullableInt(in.CallbackPort), now, now,
	)
	return err
}

func (s *supervisorsImpl) Heartbeat(ctx context.Context, id string, activeNodeCount int, tx persistence.Tx) error {
	_, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_supervisors
		   SET last_heartbeat_at = ?,
		       active_node_count = ?
		 WHERE id = ?`,
		nowUTC(), activeNodeCount, id,
	)
	return err
}

func (s *supervisorsImpl) Get(ctx context.Context, id string, tx persistence.Tx) (*persistence.SupervisorRow, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT `+supervisorCols+` FROM rimsky_supervisors WHERE id = ?`, id)
	r, err := scanSupervisor(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

func (s *supervisorsImpl) List(ctx context.Context, tx persistence.Tx) ([]persistence.SupervisorRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+supervisorCols+` FROM rimsky_supervisors
		 ORDER BY registered_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectSupervisors(rows)
}

func (s *supervisorsImpl) ListStale(ctx context.Context, cutoff time.Time, tx persistence.Tx) ([]persistence.SupervisorRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+supervisorCols+` FROM rimsky_supervisors
		 WHERE last_heartbeat_at < ?`, formatTime(cutoff))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectSupervisors(rows)
}

func (s *supervisorsImpl) Unregister(ctx context.Context, id string, tx persistence.Tx) error {
	_, err := s.q(tx).ExecContext(ctx, `DELETE FROM rimsky_supervisors WHERE id = ?`, id)
	return err
}

func scanSupervisor(sc scannable) (persistence.SupervisorRow, error) {
	var (
		r                    persistence.SupervisorRow
		acceptedExecutorsStr string
		acceptedStoresStr    string
		callbackHost         sql.NullString
		callbackPort         sql.NullInt64
		lastHeartbeatAtStr   string
		registeredAtStr      string
	)
	if err := sc.Scan(
		&r.ID, &acceptedExecutorsStr, &acceptedStoresStr, &r.Concurrency,
		&callbackHost, &callbackPort,
		&lastHeartbeatAtStr, &r.ActiveNodeCount, &registeredAtStr,
	); err != nil {
		return persistence.SupervisorRow{}, err
	}
	executors, err := unmarshalStringArray(acceptedExecutorsStr)
	if err != nil {
		return persistence.SupervisorRow{}, err
	}
	stores, err := unmarshalStringArray(acceptedStoresStr)
	if err != nil {
		return persistence.SupervisorRow{}, err
	}
	r.AcceptedExecutors = executors
	r.AcceptedStores = stores
	r.CallbackHost = callbackHost.String
	if callbackPort.Valid {
		r.CallbackPort = int(callbackPort.Int64)
	}
	lastHB, err := parseTime(lastHeartbeatAtStr)
	if err != nil {
		return persistence.SupervisorRow{}, err
	}
	r.LastHeartbeatAt = lastHB
	regAt, err := parseTime(registeredAtStr)
	if err != nil {
		return persistence.SupervisorRow{}, err
	}
	r.RegisteredAt = regAt
	if r.AcceptedExecutors == nil {
		r.AcceptedExecutors = []string{}
	}
	if r.AcceptedStores == nil {
		r.AcceptedStores = []string{}
	}
	return r, nil
}

func collectSupervisors(rows *sql.Rows) ([]persistence.SupervisorRow, error) {
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
