// SupervisorStore — port of rimsky/src/storage/postgres/supervisor-store.ts.
// Adapted for rename: `accepts` TEXT[] → `accepted_executors` TEXT[];
// `active_cell_count` → `active_node_count`.
package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/storage"
)

type SupervisorStore struct {
	pool *pgxpool.Pool
}

var _ storage.SupervisorStore = (*SupervisorStore)(nil)

const supervisorCols = `
  id, accepted_executors, concurrency, callback_host, callback_port,
  last_heartbeat_at, active_node_count, registered_at
`

func (s *SupervisorStore) Register(ctx context.Context, in storage.SupervisorRegisterInput, tx storage.Tx) error {
	ex := q(tx, s.pool)
	accepts := in.AcceptedExecutors
	if accepts == nil {
		accepts = []string{}
	}
	_, err := ex.Exec(ctx,
		`INSERT INTO rimsky_supervisors
		   (id, accepted_executors, concurrency, callback_host, callback_port, last_heartbeat_at)
		 VALUES ($1, $2, $3, $4, $5, NOW())
		 ON CONFLICT (id) DO UPDATE
		   SET accepted_executors = EXCLUDED.accepted_executors,
		       concurrency = EXCLUDED.concurrency,
		       callback_host = EXCLUDED.callback_host,
		       callback_port = EXCLUDED.callback_port,
		       last_heartbeat_at = NOW(),
		       active_node_count = 0`,
		in.ID, accepts, in.Concurrency,
		nullableString(in.CallbackHost), nullableInt(in.CallbackPort),
	)
	return err
}

func (s *SupervisorStore) Heartbeat(ctx context.Context, id string, activeNodeCount int, tx storage.Tx) error {
	ex := q(tx, s.pool)
	_, err := ex.Exec(ctx,
		`UPDATE rimsky_supervisors
		   SET last_heartbeat_at = NOW(),
		       active_node_count = $2
		 WHERE id = $1`,
		id, activeNodeCount,
	)
	return err
}

func (s *SupervisorStore) Get(ctx context.Context, id string, tx storage.Tx) (*storage.SupervisorRow, error) {
	ex := q(tx, s.pool)
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

func (s *SupervisorStore) List(ctx context.Context, tx storage.Tx) ([]storage.SupervisorRow, error) {
	ex := q(tx, s.pool)
	rows, err := ex.Query(ctx,
		`SELECT `+supervisorCols+` FROM rimsky_supervisors
		 ORDER BY registered_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectSupervisors(rows)
}

func (s *SupervisorStore) ListStale(ctx context.Context, cutoff time.Time, tx storage.Tx) ([]storage.SupervisorRow, error) {
	ex := q(tx, s.pool)
	rows, err := ex.Query(ctx,
		`SELECT `+supervisorCols+` FROM rimsky_supervisors
		 WHERE last_heartbeat_at < $1`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectSupervisors(rows)
}

func (s *SupervisorStore) Unregister(ctx context.Context, id string, tx storage.Tx) error {
	ex := q(tx, s.pool)
	_, err := ex.Exec(ctx, `DELETE FROM rimsky_supervisors WHERE id = $1`, id)
	return err
}

// ---- helpers ----

func scanSupervisor(sc scannable) (storage.SupervisorRow, error) {
	var (
		r            storage.SupervisorRow
		callbackHost *string
		callbackPort *int
	)
	if err := sc.Scan(
		&r.ID, &r.AcceptedExecutors, &r.Concurrency,
		&callbackHost, &callbackPort,
		&r.LastHeartbeatAt, &r.ActiveNodeCount, &r.RegisteredAt,
	); err != nil {
		return storage.SupervisorRow{}, err
	}
	r.CallbackHost = derefString(callbackHost)
	if callbackPort != nil {
		r.CallbackPort = *callbackPort
	}
	if r.AcceptedExecutors == nil {
		r.AcceptedExecutors = []string{}
	}
	return r, nil
}

func collectSupervisors(rows pgx.Rows) ([]storage.SupervisorRow, error) {
	var out []storage.SupervisorRow
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
