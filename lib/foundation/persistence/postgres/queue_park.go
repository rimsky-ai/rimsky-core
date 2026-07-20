// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

func (q *queueImpl) ParkActiveInTx(ctx context.Context, tx persistence.Tx, in persistence.ParkActiveInput) error {
	if tx == nil {
		return errors.New("postgres.ParkActiveInTx: tx required")
	}
	if in.ExpectedClaimedBy == "" {
		return errors.New("postgres.ParkActiveInTx: ExpectedClaimedBy required")
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
		return fmt.Errorf("postgres.ParkActiveInTx: %w", err)
	}
	if cmd.RowsAffected() != 1 {
		return fmt.Errorf("postgres.ParkActiveInTx: row %s not in expected (active, claimed_by=%s) state: %w", in.NodeRunID, in.ExpectedClaimedBy, persistence.ErrRunClaimantMismatch)
	}
	return nil
}

func (q *queueImpl) ListParkedReadyForResume(ctx context.Context, cutoff time.Time, limit int) ([]persistence.ParkedRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := q.pool.Query(ctx,
		`SELECT d.id, d.node_id, d.executor_name, d.required_stores, d.frame_id,
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

func (q *queueImpl) GetParkedByNode(ctx context.Context, tx persistence.Tx, nodeID shared.UUID, runScopeID shared.UUID) (*persistence.ParkedRow, error) {
	row := q.q(tx).QueryRow(ctx,
		`SELECT d.id, d.node_id, d.executor_name, d.required_stores, d.frame_id,
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

func (q *queueImpl) ResumeParkedInTx(ctx context.Context, tx persistence.Tx, nodeRunID shared.UUID) (bool, error) {
	if tx == nil {
		return false, errors.New("postgres.ResumeParkedInTx: tx required")
	}
	cmd, err := q.q(tx).Exec(ctx,
		`UPDATE rimsky_node_runs
		    SET claimed_by = NULL,
		        claimed_at = NULL,
		        resume_at = NULL
		  WHERE id = $1
		    AND state = 'parked'`,
		nodeRunID,
	)
	if err != nil {
		return false, fmt.Errorf("postgres.ResumeParkedInTx: %w", err)
	}
	return cmd.RowsAffected() == 1, nil
}

func (q *queueImpl) UpdateDispatchTuningInTx(ctx context.Context, tx persistence.Tx, nodeRunID shared.UUID, maxRetriesWithoutProgress *int) error {
	_, err := q.q(tx).Exec(ctx,
		`UPDATE rimsky_node_runs
		    SET max_retries_without_progress = $2
		  WHERE id = $1`,
		nodeRunID, intPtrOrNullPark(maxRetriesWithoutProgress),
	)
	if err != nil {
		return fmt.Errorf("postgres.UpdateDispatchTuningInTx: %w", err)
	}
	return nil
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
		r        persistence.ParkedRow
		executor sql.NullString
		stores   []string
		resumeAt sql.NullTime
	)
	if err := row.Scan(
		&r.NodeRunID, &r.NodeID, &executor, &stores, &r.FrameID,
		&r.ParkedAt, &resumeAt, &r.ConsecutiveRetriesNoProg,
	); err != nil {
		return nil, err
	}
	if executor.Valid {
		r.ExecutorName = executor.String
	}
	if stores == nil {
		stores = []string{}
	}
	r.RequiredClaimProducers = stores
	if resumeAt.Valid {
		t := resumeAt.Time
		r.ResumeAt = &t
	}
	return &r, nil
}

func (q *queueImpl) LoadScratchInTx(ctx context.Context, tx persistence.Tx, nodeRunID shared.UUID) ([]byte, string, string, error) {
	if tx == nil {
		return nil, "", "", errors.New("postgres.LoadScratchInTx: tx required")
	}
	var (
		inline  []byte
		handle  sql.NullString
		backend sql.NullString
	)
	err := q.q(tx).QueryRow(ctx,
		`SELECT scratch_inline, scratch_handle, scratch_handle_backend
		   FROM rimsky_node_runs
		  WHERE id = $1`,
		nodeRunID,
	).Scan(&inline, &handle, &backend)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, "", "", nil
		}
		return nil, "", "", fmt.Errorf("postgres.LoadScratchInTx: %w", err)
	}
	var hStr, bStr string
	if handle.Valid {
		hStr = handle.String
	}
	if backend.Valid {
		bStr = backend.String
	}
	return inline, hStr, bStr, nil
}

// @concept: blob-backend
func (q *queueImpl) WriteScratchInTx(ctx context.Context, tx persistence.Tx, nodeRunID shared.UUID, inline []byte, handle, handleBackend string) error {
	if tx == nil {
		return errors.New("postgres.WriteScratchInTx: tx required")
	}
	if len(inline) > 0 && handle != "" {
		return errors.New("postgres.WriteScratchInTx: inline and handle are mutually exclusive")
	}
	if q.tables == nil {
		return errors.New("postgres.WriteScratchInTx: queue not wired with tables")
	}
	var priorHandle, priorBackend sql.NullString
	if err := q.q(tx).QueryRow(ctx,
		`SELECT scratch_handle, scratch_handle_backend FROM rimsky_node_runs WHERE id = $1`,
		nodeRunID,
	).Scan(&priorHandle, &priorBackend); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("postgres.WriteScratchInTx: %s: %w", nodeRunID, persistence.ErrRunRowMissing)
		}
		return fmt.Errorf("postgres.WriteScratchInTx: read prior handle: %w", err)
	}

	tag, err := q.q(tx).Exec(ctx,
		`UPDATE rimsky_node_runs
		    SET scratch_inline         = $2,
		        scratch_handle         = $3,
		        scratch_handle_backend = $4
		  WHERE id = $1`,
		nodeRunID, nilIfEmpty(inline), nilIfEmptyStr(handle), nilIfEmptyStr(handleBackend),
	)
	if err != nil {
		return fmt.Errorf("postgres.WriteScratchInTx: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres.WriteScratchInTx: %s: %w", nodeRunID, persistence.ErrRunRowMissing)
	}
	if priorHandle.Valid && priorHandle.String != "" && priorHandle.String != handle {
		if err := persistence.QueueBlobOrphan(ctx, q.tables.BlobOrphans(), tx,
			priorHandle.String, priorBackend.String, time.Now().UTC(), q.tables.blobRetention); err != nil {
			return fmt.Errorf("postgres.WriteScratchInTx: queue prior orphan: %w", err)
		}
	}
	return nil
}

func timeOrNullPark(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func intPtrOrNullPark(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
