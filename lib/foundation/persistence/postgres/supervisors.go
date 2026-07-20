// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

const supervisorCols = `
  id, accepted_executors, accepted_claim_producers, concurrency, callback_host, callback_port,
  registered_at
`

func (s *supervisorsImpl) Register(ctx context.Context, in persistence.SupervisorRegisterInput, tx persistence.Tx) error {
	ex := s.q(tx)
	accepts := in.AcceptedExecutors
	if accepts == nil {
		accepts = []string{}
	}
	stores := in.AcceptedClaimProducers
	if stores == nil {
		stores = []string{}
	}
	_, err := ex.Exec(ctx,
		`INSERT INTO rimsky_supervisors
		   (id, accepted_executors, accepted_claim_producers, concurrency, callback_host, callback_port)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (id) DO UPDATE
		   SET accepted_executors      = EXCLUDED.accepted_executors,
		       accepted_claim_producers = EXCLUDED.accepted_claim_producers,
		       concurrency              = EXCLUDED.concurrency,
		       callback_host            = EXCLUDED.callback_host,
		       callback_port            = EXCLUDED.callback_port`,
		in.ID, accepts, stores, in.Concurrency,
		nullableString(in.CallbackHost), nullableInt(in.CallbackPort),
	)
	if err != nil {
		return fmt.Errorf("postgres.Supervisors.Register: %w", err)
	}
	return nil
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
		return nil, fmt.Errorf("postgres.Supervisors.Get: %w", err)
	}
	return &r, nil
}

func (s *supervisorsImpl) List(ctx context.Context, tx persistence.Tx) ([]persistence.SupervisorRow, error) {
	ex := s.q(tx)
	rows, err := ex.Query(ctx,
		`SELECT `+supervisorCols+` FROM rimsky_supervisors
		 ORDER BY registered_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("postgres.Supervisors.List: %w", err)
	}
	defer rows.Close()
	out, err := collectSupervisors(rows)
	if err != nil {
		return nil, fmt.Errorf("postgres.Supervisors.List: %w", err)
	}
	return out, nil
}

func (s *supervisorsImpl) Unregister(ctx context.Context, id string, tx persistence.Tx) error {
	ex := s.q(tx)
	if _, err := ex.Exec(ctx, `DELETE FROM rimsky_supervisors WHERE id = $1`, id); err != nil {
		return fmt.Errorf("postgres.Supervisors.Unregister: %w", err)
	}
	return nil
}

func scanSupervisor(sc scannable) (persistence.SupervisorRow, error) {
	var (
		r            persistence.SupervisorRow
		callbackHost *string
		callbackPort *int
	)
	if err := sc.Scan(
		&r.ID, &r.AcceptedExecutors, &r.AcceptedClaimProducers, &r.Concurrency,
		&callbackHost, &callbackPort,
		&r.RegisteredAt,
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
	if r.AcceptedClaimProducers == nil {
		r.AcceptedClaimProducers = []string{}
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
