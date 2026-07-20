// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

func (q *queueImpl) ParkActiveInTx(ctx context.Context, tx persistence.Tx, in persistence.ParkActiveInput) error {
	if tx == nil {
		return errors.New("sqlite.ParkActiveInTx: tx required")
	}
	if in.ExpectedClaimedBy == "" {
		return errors.New("sqlite.ParkActiveInTx: ExpectedClaimedBy required")
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
		return fmt.Errorf("sqlite.ParkActiveInTx: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite.ParkActiveInTx: rows affected: %w", err)
	}
	if rowsAffected != 1 {
		return fmt.Errorf("sqlite.ParkActiveInTx: row %s not in expected (active, claimed_by=%s) state: %w", in.NodeRunID, in.ExpectedClaimedBy, persistence.ErrRunClaimantMismatch)
	}
	return nil
}

func (q *queueImpl) ListParkedReadyForResume(ctx context.Context, cutoff time.Time, limit int) ([]persistence.ParkedRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := q.db.QueryContext(ctx,
		`SELECT d.id, d.node_id, d.executor_name, d.required_stores, d.frame_id,
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
func (q *queueImpl) GetParkedByNode(ctx context.Context, tx persistence.Tx, nodeID shared.UUID, runScopeID shared.UUID) (*persistence.ParkedRow, error) {
	row := q.q(tx).QueryRowContext(ctx,
		`SELECT d.id, d.node_id, d.executor_name, d.required_stores, d.frame_id,
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

func (q *queueImpl) ResumeParkedInTx(ctx context.Context, tx persistence.Tx, nodeRunID shared.UUID) (bool, error) {
	if tx == nil {
		return false, errors.New("sqlite.ResumeParkedInTx: tx required")
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
		return false, fmt.Errorf("sqlite.ResumeParkedInTx: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("sqlite.ResumeParkedInTx: rows affected: %w", err)
	}
	return rowsAffected == 1, nil
}

func (q *queueImpl) UpdateDispatchTuningInTx(ctx context.Context, tx persistence.Tx, nodeRunID shared.UUID, maxRetriesWithoutProgress *int) error {
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
		return fmt.Errorf("sqlite.UpdateDispatchTuningInTx: %w", err)
	}
	return nil
}

// @concept: executor
func (q *queueImpl) LoadScratchInTx(ctx context.Context, tx persistence.Tx, nodeRunID shared.UUID) ([]byte, string, string, error) {
	if tx == nil {
		return nil, "", "", errors.New("sqlite.LoadScratchInTx: tx required")
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
		return nil, "", "", fmt.Errorf("sqlite.LoadScratchInTx: %w", err)
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
func (q *queueImpl) WriteScratchInTx(ctx context.Context, tx persistence.Tx, nodeRunID shared.UUID, inline []byte, handle, handleBackend string) error {
	if tx == nil {
		return errors.New("sqlite.WriteScratchInTx: tx required")
	}
	if len(inline) > 0 && handle != "" {
		return errors.New("sqlite.WriteScratchInTx: inline and handle are mutually exclusive")
	}
	if q.tables == nil {
		return errors.New("sqlite.WriteScratchInTx: queue not wired with tables")
	}
	var priorHandle, priorBackend sql.NullString
	if err := q.q(tx).QueryRowContext(ctx,
		`SELECT scratch_handle, scratch_handle_backend FROM rimsky_node_runs WHERE id = ?`,
		nodeRunID.String(),
	).Scan(&priorHandle, &priorBackend); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("sqlite.WriteScratchInTx: %s: %w", nodeRunID, persistence.ErrRunRowMissing)
		}
		return fmt.Errorf("sqlite.WriteScratchInTx: read prior handle: %w", err)
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
		return fmt.Errorf("sqlite.WriteScratchInTx: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite.WriteScratchInTx: rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("sqlite.WriteScratchInTx: %s: %w", nodeRunID, persistence.ErrRunRowMissing)
	}
	if priorHandle.Valid && priorHandle.String != "" && priorHandle.String != handle {
		if err := persistence.QueueBlobOrphan(ctx, q.tables.BlobOrphans(), tx,
			priorHandle.String, priorBackend.String, time.Now().UTC(), q.tables.blobRetention); err != nil {
			return fmt.Errorf("sqlite.WriteScratchInTx: queue prior orphan: %w", err)
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
		storesStr          string
		frameIDStr         string
		parkedAtStr        string
		resumeAtStr        sql.NullString
		consecutiveRetries int
	)
	if err := row.Scan(
		&idStr, &nodeIDStr, &executor, &storesStr, &frameIDStr,
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
	stores, err := unmarshalStringArray(storesStr)
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
		RequiredClaimProducers:   stores,
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
