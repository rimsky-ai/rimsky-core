// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func (q *queueImpl) ParkActive(ctx context.Context, in persistence.ParkActiveInput, tx persistence.Tx) error {
	if tx == nil {
		return errors.New("sqlite.ParkActive: tx required")
	}
	if in.ExpectedClaimedBy == "" {
		return errors.New("sqlite.ParkActive: ExpectedClaimedBy required")
	}
	var resumeAt any
	if !in.ResumeAt.IsZero() {
		resumeAt = formatTime(in.ResumeAt)
	}
	res, err := q.q(tx).ExecContext(ctx,
		`UPDATE rimsky_node_runs
		    SET claimed_by = NULL,
		        claimed_at = NULL,
		        parked_at = ?,
		        resume_at = ?
		  WHERE id = ?
		    AND claimed_by = ?
		    AND state = 'running'`,
		formatTime(in.ParkedAt), resumeAt,
		in.NodeRunID.String(), in.ExpectedClaimedBy,
	)
	if err != nil {
		return fmt.Errorf("sqlite.ParkActive: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite.ParkActive: rows affected: %w", err)
	}
	if rowsAffected != 1 {
		return fmt.Errorf("sqlite.ParkActive: row %s not in expected (active, claimed_by=%s) state: %w", in.NodeRunID, in.ExpectedClaimedBy, persistence.ErrRunClaimantMismatch)
	}
	return nil
}

func (q *queueImpl) ListParkedReadyForResume(ctx context.Context, cutoff time.Time, limit int) ([]persistence.ParkedRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := q.db.QueryContext(ctx,
		`SELECT d.id, d.node_id, d.executor_name, d.required_claim_producers, d.frame_id,
		        d.parked_at, d.resume_at, d.consecutive_retries_no_progress
		   FROM rimsky_node_runs d
		  WHERE d.state = 'parked'
		    AND d.resume_at IS NOT NULL
		    AND d.resume_at <= ?
		  ORDER BY d.resume_at ASC
		  LIMIT ?`,
		formatTime(cutoff), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite.ListParkedReadyForResume: %w", err)
	}
	defer rows.Close()
	return scanSqliteParkedRows(rows)
}

func (q *queueImpl) ListParkedDiagnostic(ctx context.Context, tx persistence.Tx) ([]persistence.ParkedDiagnosticRow, error) {
	if tx == nil {
		return nil, errors.New("sqlite.ListParkedDiagnostic: tx required")
	}
	rows, err := q.q(tx).QueryContext(ctx,
		`SELECT d.id, n.instance_id, d.node_id, d.frame_id,
		        d.parked_at, d.resume_at
		   FROM rimsky_node_runs d
		   LEFT JOIN rimsky_nodes n ON n.id = d.node_id
		  WHERE d.state = 'parked'
		  ORDER BY d.parked_at ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite.ListParkedDiagnostic: %w", err)
	}
	defer rows.Close()
	var out []persistence.ParkedDiagnosticRow
	for rows.Next() {
		var (
			r           persistence.ParkedDiagnosticRow
			dispatchStr string
			instID      sql.NullString
			frameID     sql.NullString
			parkedAtStr string
			resumeAtStr sql.NullString
			nodeID      string
		)
		if err := rows.Scan(&dispatchStr, &instID, &nodeID, &frameID, &parkedAtStr, &resumeAtStr); err != nil {
			return nil, err
		}
		nodeRunID, err := uuid.Parse(dispatchStr)
		if err != nil {
			return nil, fmt.Errorf("sqlite.ListParkedDiagnostic: parse dispatch id: %w", err)
		}
		r.NodeRunID = nodeRunID
		if instID.Valid {
			r.InstanceID = instID.String
		}
		r.NodeID = nodeID
		if frameID.Valid {
			r.FrameID = frameID.String
		}
		t, err := parseTime(parkedAtStr)
		if err != nil {
			return nil, fmt.Errorf("sqlite.ListParkedDiagnostic: parse parked_at: %w", err)
		}
		r.ParkedAt = t
		if resumeAtStr.Valid {
			rt, err := parseTime(resumeAtStr.String)
			if err != nil {
				return nil, fmt.Errorf("sqlite.ListParkedDiagnostic: parse resume_at: %w", err)
			}
			r.ResumeAt = rt
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// @concept: run-scope
func (q *queueImpl) GetParkedByNode(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID, tx persistence.Tx) (*persistence.ParkedRow, error) {
	row := q.q(tx).QueryRowContext(ctx,
		`SELECT d.id, d.node_id, d.executor_name, d.required_claim_producers, d.frame_id,
		        d.parked_at, d.resume_at, d.consecutive_retries_no_progress
		   FROM rimsky_node_runs d
		  WHERE d.node_id = ?
		    AND d.run_scope_id = ?
		    AND d.state = 'parked'`,
		nodeID.String(), runScopeID.String(),
	)
	r, err := scanOneSqliteParkedRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite.GetParkedByNode: %w", err)
	}
	return r, nil
}

func (q *queueImpl) ResumeParked(ctx context.Context, nodeRunID shared.UUID, tx persistence.Tx) (bool, error) {
	if tx == nil {
		return false, errors.New("sqlite.ResumeParked: tx required")
	}
	res, err := q.q(tx).ExecContext(ctx,
		`UPDATE rimsky_node_runs
		    SET claimed_by = NULL,
		        claimed_at = NULL,
		        parked_at = NULL,
		        resume_at = NULL
		  WHERE id = ?
		    AND state = 'parked'`,
		nodeRunID.String(),
	)
	if err != nil {
		return false, fmt.Errorf("sqlite.ResumeParked: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("sqlite.ResumeParked: rows affected: %w", err)
	}
	return rowsAffected == 1, nil
}

func (q *queueImpl) UpdateDispatchTuning(ctx context.Context, nodeRunID shared.UUID, maxRetriesWithoutProgress *int, tx persistence.Tx) error {
	var retries any
	if maxRetriesWithoutProgress != nil {
		retries = *maxRetriesWithoutProgress
	}
	_, err := q.q(tx).ExecContext(ctx,
		`UPDATE rimsky_node_runs
		    SET max_retries_without_progress = ?
		  WHERE id = ?`,
		retries, nodeRunID.String(),
	)
	if err != nil {
		return fmt.Errorf("sqlite.UpdateDispatchTuning: %w", err)
	}
	return nil
}

// @concept: executor
func (q *queueImpl) LoadScratch(ctx context.Context, nodeRunID shared.UUID, tx persistence.Tx) ([]byte, string, string, error) {
	if tx == nil {
		return nil, "", "", errors.New("sqlite.LoadScratch: tx required")
	}
	var (
		inline  []byte
		handle  sql.NullString
		backend sql.NullString
	)
	err := q.q(tx).QueryRowContext(ctx,
		`SELECT scratch_inline, scratch_handle, scratch_handle_backend
		   FROM rimsky_node_runs
		  WHERE id = ?`,
		nodeRunID.String(),
	).Scan(&inline, &handle, &backend)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "", "", nil
		}
		return nil, "", "", fmt.Errorf("sqlite.LoadScratch: %w", err)
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

// @concept: executor
// @concept: blob-backend
func (q *queueImpl) WriteScratch(ctx context.Context, nodeRunID shared.UUID, inline []byte, handle, handleBackend string, tx persistence.Tx) error {
	if tx == nil {
		return errors.New("sqlite.WriteScratch: tx required")
	}
	if len(inline) > 0 && handle != "" {
		return errors.New("sqlite.WriteScratch: inline and handle are mutually exclusive")
	}
	if q.tables == nil {
		return errors.New("sqlite.WriteScratch: queue not wired with tables")
	}
	var priorHandle, priorBackend sql.NullString
	if err := q.q(tx).QueryRowContext(ctx,
		`SELECT scratch_handle, scratch_handle_backend FROM rimsky_node_runs WHERE id = ?`,
		nodeRunID.String(),
	).Scan(&priorHandle, &priorBackend); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("sqlite.WriteScratch: %s: %w", nodeRunID, persistence.ErrNotFound)
		}
		return fmt.Errorf("sqlite.WriteScratch: read prior handle: %w", err)
	}

	var inlineArg any
	if len(inline) > 0 {
		inlineArg = inline
	}
	res, err := q.q(tx).ExecContext(ctx,
		`UPDATE rimsky_node_runs
		    SET scratch_inline         = ?,
		        scratch_handle         = ?,
		        scratch_handle_backend = ?
		  WHERE id = ?`,
		inlineArg, nullableString(handle), nullableString(handleBackend), nodeRunID.String(),
	)
	if err != nil {
		return fmt.Errorf("sqlite.WriteScratch: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite.WriteScratch: rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("sqlite.WriteScratch: %s: %w", nodeRunID, persistence.ErrNotFound)
	}
	if priorHandle.Valid && priorHandle.String != "" && priorHandle.String != handle {
		if err := persistence.QueueBlobOrphan(ctx, q.tables.BlobOrphans(), priorHandle.String, priorBackend.String, time.Now().UTC(), q.tables.blobRetention, tx); err != nil {
			return fmt.Errorf("sqlite.WriteScratch: queue prior orphan: %w", err)
		}
	}
	return nil
}

func scanSqliteParkedRows(rows *sql.Rows) ([]persistence.ParkedRow, error) {
	var out []persistence.ParkedRow
	for rows.Next() {
		r, err := scanOneSqliteParkedRow(rows)
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

func scanOneSqliteParkedRow(row scannable) (*persistence.ParkedRow, error) {
	var (
		idStr, nodeIDStr   string
		executor           sql.NullString
		claimProducersStr  string
		frameIDStr         string
		parkedAtStr        string
		resumeAtStr        sql.NullString
		consecutiveRetries int
	)
	if err := row.Scan(
		&idStr, &nodeIDStr, &executor, &claimProducersStr, &frameIDStr,
		&parkedAtStr, &resumeAtStr, &consecutiveRetries,
	); err != nil {
		return nil, err
	}
	disp, err := parseUUID(idStr)
	if err != nil {
		return nil, err
	}
	node, err := parseUUID(nodeIDStr)
	if err != nil {
		return nil, err
	}
	frame, err := parseUUID(frameIDStr)
	if err != nil {
		return nil, err
	}
	claimProducers, err := unmarshalStringArray(claimProducersStr)
	if err != nil {
		return nil, err
	}
	parkedAt, err := parseTime(parkedAtStr)
	if err != nil {
		return nil, err
	}
	out := &persistence.ParkedRow{
		NodeRunID:                disp,
		NodeID:                   node,
		FrameID:                  frame,
		ParkedAt:                 parkedAt,
		ConsecutiveRetriesNoProg: consecutiveRetries,
		RequiredClaimProducers:   claimProducers,
	}
	if executor.Valid {
		out.ExecutorName = executor.String
	}
	if resumeAtStr.Valid {
		t, err := parseTime(resumeAtStr.String)
		if err != nil {
			return nil, err
		}
		out.ResumeAt = &t
	}
	return out, nil
}
