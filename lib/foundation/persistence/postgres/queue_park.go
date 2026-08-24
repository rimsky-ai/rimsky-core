// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func (q *queueImpl) ParkActive(ctx context.Context, in persistence.ParkActiveInput, tx persistence.Tx) error {
	if tx == nil {
		return errors.New("postgres.ParkActive: tx required")
	}
	if in.ExpectedClaimedBy == "" {
		return errors.New("postgres.ParkActive: ExpectedClaimedBy required")
	}
	resumeAt := timeOrNullPark(in.ResumeAt)

	cmd, err := q.q(tx).Exec(ctx,
		`UPDATE rimsky_node_runs
		    SET claimed_by = NULL,
		        claimed_at = NULL,
		        parked_at = $3,
		        resume_at = $4
		  WHERE id = $1
		    AND claimed_by = $2
		    AND state = 'running'`,
		in.NodeRunID, in.ExpectedClaimedBy, in.ParkedAt, resumeAt,
	)
	if err != nil {
		return fmt.Errorf("postgres.ParkActive: %w", err)
	}
	if cmd.RowsAffected() != 1 {
		return fmt.Errorf("postgres.ParkActive: row %s not in expected (active, claimed_by=%s) state: %w", in.NodeRunID, in.ExpectedClaimedBy, persistence.ErrRunClaimantMismatch)
	}
	return nil
}

func (q *queueImpl) ListParkedReadyForResume(ctx context.Context, cutoff time.Time, limit int) ([]persistence.ParkedRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := q.pool.Query(ctx,
		`SELECT d.id, d.node_id, d.executor_name, d.required_claim_producers, d.frame_id,
		        d.parked_at, d.resume_at, d.consecutive_retries_no_progress
		   FROM rimsky_node_runs d
		  WHERE d.state = 'parked'
		    AND d.resume_at IS NOT NULL
		    AND d.resume_at <= $1
		  ORDER BY d.resume_at ASC
		  LIMIT $2`,
		cutoff, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("postgres.ListParkedReadyForResume: %w", err)
	}
	defer rows.Close()
	return scanParkedRows(rows)
}

func (q *queueImpl) ListParkedDiagnostic(ctx context.Context, tx persistence.Tx) ([]persistence.ParkedDiagnosticRow, error) {
	if tx == nil {
		return nil, errors.New("postgres.ListParkedDiagnostic: tx required")
	}
	rows, err := q.q(tx).Query(ctx,
		`SELECT d.id, n.instance_id, d.node_id, d.frame_id,
		        d.parked_at, d.resume_at
		   FROM rimsky_node_runs d
		   LEFT JOIN rimsky_nodes n ON n.id = d.node_id
		  WHERE d.state = 'parked'
		  ORDER BY d.parked_at ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("postgres.ListParkedDiagnostic: %w", err)
	}
	defer rows.Close()
	var out []persistence.ParkedDiagnosticRow
	for rows.Next() {
		var (
			r        persistence.ParkedDiagnosticRow
			instID   sql.NullString
			frameID  sql.NullString
			resumeAt sql.NullTime
			nodeID   string
		)
		if err := rows.Scan(&r.NodeRunID, &instID, &nodeID, &frameID, &r.ParkedAt, &resumeAt); err != nil {
			return nil, err
		}
		if instID.Valid {
			r.InstanceID = instID.String
		}
		r.NodeID = nodeID
		if frameID.Valid {
			r.FrameID = frameID.String
		}
		if resumeAt.Valid {
			r.ResumeAt = resumeAt.Time
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (q *queueImpl) GetParkedByNode(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID, tx persistence.Tx) (*persistence.ParkedRow, error) {
	row := q.q(tx).QueryRow(ctx,
		`SELECT d.id, d.node_id, d.executor_name, d.required_claim_producers, d.frame_id,
		        d.parked_at, d.resume_at, d.consecutive_retries_no_progress
		   FROM rimsky_node_runs d
		  WHERE d.node_id = $1
		    AND d.run_scope_id = $2
		    AND d.state = 'parked'`,
		nodeID, runScopeID,
	)
	r, err := scanOneParkedRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres.GetParkedByNode: %w", err)
	}
	return r, nil
}

func (q *queueImpl) ResumeParked(ctx context.Context, nodeRunID shared.UUID, tx persistence.Tx) (bool, error) {
	if tx == nil {
		return false, errors.New("postgres.ResumeParked: tx required")
	}
	cmd, err := q.q(tx).Exec(ctx,
		`UPDATE rimsky_node_runs
		    SET claimed_by = NULL,
		        claimed_at = NULL,
		        parked_at = NULL,
		        resume_at = NULL,
		        park_resumed_at = now()
		  WHERE id = $1
		    AND state = 'parked'`,
		nodeRunID,
	)
	if err != nil {
		return false, fmt.Errorf("postgres.ResumeParked: %w", err)
	}
	return cmd.RowsAffected() == 1, nil
}

func scanParkedRows(rows pgx.Rows) ([]persistence.ParkedRow, error) {
	var out []persistence.ParkedRow
	for rows.Next() {
		r, err := scanOneParkedRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func scanOneParkedRow(row pgx.Row) (*persistence.ParkedRow, error) {
	var (
		r              persistence.ParkedRow
		executor       sql.NullString
		claimProducers []string
		resumeAt       sql.NullTime
	)
	if err := row.Scan(
		&r.NodeRunID, &r.NodeID, &executor, &claimProducers, &r.FrameID,
		&r.ParkedAt, &resumeAt, &r.ConsecutiveRetriesNoProg,
	); err != nil {
		return nil, err
	}
	if executor.Valid {
		r.ExecutorName = executor.String
	}
	if claimProducers == nil {
		claimProducers = []string{}
	}
	r.RequiredClaimProducers = claimProducers
	if resumeAt.Valid {
		t := resumeAt.Time
		r.ResumeAt = &t
	}
	return &r, nil
}

// @decision: scratch-column
func (q *queueImpl) LoadScratch(ctx context.Context, nodeRunID shared.UUID, tx persistence.Tx) ([]byte, error) {
	if tx == nil {
		return nil, errors.New("postgres.LoadScratch: tx required")
	}
	var scratch []byte
	err := q.q(tx).QueryRow(ctx,
		`SELECT scratch FROM rimsky_node_runs WHERE id = $1`,
		nodeRunID,
	).Scan(&scratch)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("postgres.LoadScratch: %w", err)
	}
	return scratch, nil
}

// @decision: scratch-column
// @decision: attribute-bytes-in-the-row
func (q *queueImpl) WriteScratch(ctx context.Context, nodeRunID shared.UUID, scratch []byte, tx persistence.Tx) error {
	if tx == nil {
		return errors.New("postgres.WriteScratch: tx required")
	}
	if err := persistence.CheckValueSize("postgres.WriteScratch", nodeRunID, "scratch", len(scratch)); err != nil {
		return err
	}
	tag, err := q.q(tx).Exec(ctx,
		`UPDATE rimsky_node_runs SET scratch = $2 WHERE id = $1`,
		nodeRunID, nilIfEmpty(scratch),
	)
	if err != nil {
		return fmt.Errorf("postgres.WriteScratch: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres.WriteScratch: %s: %w", nodeRunID, persistence.ErrNotFound)
	}
	return nil
}

func timeOrNullPark(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
