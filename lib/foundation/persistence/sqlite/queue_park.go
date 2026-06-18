// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @source: lib/foundation/persistence/postgres/queue_park.go
// @diverged: true
// @reason: parallel driver — SQLite dialect (positional params, database/sql, immediate-mode tx subsumes per-row locking) vs Postgres (pgx, $-params, explicit FOR UPDATE)

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
		    SET phase = 'parked',
		        claimed_by = NULL,
		        claimed_at = NULL,
		        parked_at = ?,
		        resume_at = ?,
		        parked_reason = ?,
		        parked_reason_note = ?,
		        parked_reason_label = ?
		  WHERE id = ?
		    AND claimed_by = ?
		    AND phase = 'active'`,
		formatTime(in.ParkedAt), resumeAt,
		nullableString(in.Reason), nullableString(in.ReasonNote),
		nullableString(in.ReasonLabel),
		in.DispatchID.String(), in.ExpectedClaimedBy,
	)
	if err != nil {
		return fmt.Errorf("sqlite.ParkActiveInTx: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite.ParkActiveInTx: rows affected: %w", err)
	}
	if rowsAffected != 1 {
		return fmt.Errorf("sqlite.ParkActiveInTx: row %s not in expected (active, claimed_by=%s) state", in.DispatchID, in.ExpectedClaimedBy)
	}
	return nil
}

func (q *queueImpl) ListParkedReadyForResume(ctx context.Context, cutoff time.Time, limit int) ([]persistence.ParkedRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := q.db.QueryContext(ctx,
		`SELECT d.id, d.node_id, d.executor_name, d.required_stores, d.frame_id,
		        d.parked_at, d.resume_at, d.parked_reason, d.parked_reason_note,
		        d.max_park_duration_seconds, d.consecutive_retries_no_progress
		   FROM rimsky_node_runs d
		  WHERE d.phase = 'parked'
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

func (q *queueImpl) ListParkedOverdue(ctx context.Context, now time.Time, limit int) ([]persistence.ParkedRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := q.db.QueryContext(ctx,
		`SELECT d.id, d.node_id, d.executor_name, d.required_stores, d.frame_id,
		        d.parked_at, d.resume_at, d.parked_reason, d.parked_reason_note,
		        d.max_park_duration_seconds, d.consecutive_retries_no_progress
		   FROM rimsky_node_runs d
		  WHERE d.phase = 'parked'
		    AND d.max_park_duration_seconds IS NOT NULL
		    AND d.parked_at IS NOT NULL
		  ORDER BY d.parked_at ASC
		  LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite.ListParkedOverdue: %w", err)
	}
	defer rows.Close()
	all, err := scanSqliteParkedRows(rows)
	if err != nil {
		return nil, err
	}
	out := make([]persistence.ParkedRow, 0, len(all))
	for _, r := range all {
		if r.MaxParkDurationSeconds == nil {
			continue
		}
		deadline := r.ParkedAt.Add(time.Duration(*r.MaxParkDurationSeconds) * time.Second)
		if deadline.After(now) {
			continue
		}
		if r.ResumeAt != nil && !r.ResumeAt.After(now) {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func (q *queueImpl) ListParkedDiagnostic(ctx context.Context, tx persistence.Tx, reasonFilter string) ([]persistence.ParkedDiagnosticRow, error) {
	if tx == nil {
		return nil, errors.New("sqlite.ListParkedDiagnostic: tx required")
	}
	var reasonArg any
	if reasonFilter != "" {
		reasonArg = reasonFilter
	}
	rows, err := q.q(tx).QueryContext(ctx,
		`SELECT d.id, n.instance_id, d.node_id, d.frame_id,
		        d.parked_at, d.resume_at, d.parked_reason, d.parked_reason_note
		   FROM rimsky_node_runs d
		   LEFT JOIN rimsky_nodes n ON n.id = d.node_id
		  WHERE d.phase = 'parked'
		    AND (? IS NULL OR d.parked_reason = ?)
		  ORDER BY d.parked_at ASC`,
		reasonArg, reasonArg,
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
			reason      sql.NullString
			reasonNote  sql.NullString
			nodeID      string
		)
		if err := rows.Scan(&dispatchStr, &instID, &nodeID, &frameID, &parkedAtStr, &resumeAtStr, &reason, &reasonNote); err != nil {
			return nil, err
		}
		dispatchID, err := uuid.Parse(dispatchStr)
		if err != nil {
			return nil, fmt.Errorf("sqlite.ListParkedDiagnostic: parse dispatch id: %w", err)
		}
		r.DispatchID = dispatchID
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
		if reason.Valid {
			r.Reason = reason.String
		}
		if reasonNote.Valid {
			r.ReasonNote = reasonNote.String
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// @concept: run-scope
func (q *queueImpl) GetParkedByNode(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID) (*persistence.ParkedRow, error) {
	row := q.db.QueryRowContext(ctx,
		`SELECT d.id, d.node_id, d.executor_name, d.required_stores, d.frame_id,
		        d.parked_at, d.resume_at, d.parked_reason, d.parked_reason_note,
		        d.max_park_duration_seconds, d.consecutive_retries_no_progress
		   FROM rimsky_node_runs d
		  WHERE d.node_id = ?
		    AND d.run_scope_id = ?
		    AND d.phase = 'parked'`,
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

func (q *queueImpl) ResumeParkedInTx(ctx context.Context, tx persistence.Tx, dispatchID shared.UUID) (bool, error) {
	if tx == nil {
		return false, errors.New("sqlite.ResumeParkedInTx: tx required")
	}
	res, err := q.q(tx).ExecContext(ctx,
		`UPDATE rimsky_node_runs
		    SET phase = 'pending',
		        claimed_by = NULL,
		        claimed_at = NULL,
		        resume_at = NULL
		  WHERE id = ?
		    AND phase = 'parked'`,
		dispatchID.String(),
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

func (q *queueImpl) RebindRunFrameInTx(
	ctx context.Context, tx persistence.Tx,
	dispatchID, newFrameID shared.UUID,
) error {
	if tx == nil {
		return errors.New("sqlite.RebindRunFrameInTx: tx required")
	}
	res, err := q.q(tx).ExecContext(ctx,
		`UPDATE rimsky_node_runs SET frame_id = ? WHERE id = ?`,
		newFrameID.String(), dispatchID.String(),
	)
	if err != nil {
		return fmt.Errorf("sqlite.RebindRunFrameInTx: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite.RebindRunFrameInTx: rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("sqlite.RebindRunFrameInTx: %s: %w", dispatchID, persistence.ErrRunRowMissing)
	}
	return nil
}

func (q *queueImpl) GetRetryNoProgress(ctx context.Context, dispatchID shared.UUID) (int, *int, error) {
	var (
		count    int
		override sql.NullInt64
	)
	err := q.db.QueryRowContext(ctx,
		`SELECT consecutive_retries_no_progress, max_retries_without_progress
		   FROM rimsky_node_runs
		  WHERE id = ?`,
		dispatchID.String(),
	).Scan(&count, &override)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil, nil
		}
		return 0, nil, fmt.Errorf("sqlite.GetRetryNoProgress: %w", err)
	}
	if override.Valid {
		v := int(override.Int64)
		return count, &v, nil
	}
	return count, nil, nil
}

// @concept: run-scope
func (q *queueImpl) SetRetryNoProgressForNodeInTx(ctx context.Context, tx persistence.Tx, nodeID shared.UUID, runScopeID shared.UUID, count int) error {
	if tx == nil {
		return errors.New("sqlite.SetRetryNoProgressForNodeInTx: tx required")
	}
	_, err := q.q(tx).ExecContext(ctx,
		`UPDATE rimsky_node_runs
		    SET consecutive_retries_no_progress = ?
		  WHERE node_id = ?
		    AND run_scope_id = ?
		    AND phase = 'pending'
		    AND claimed_by IS NULL`,
		count, nodeID.String(), runScopeID.String(),
	)
	if err != nil {
		return fmt.Errorf("sqlite.SetRetryNoProgressForNodeInTx: %w", err)
	}
	return nil
}

func (q *queueImpl) UpdateDispatchTuningInTx(ctx context.Context, tx persistence.Tx, dispatchID shared.UUID, maxParkDurationSeconds *int, maxRetriesWithoutProgress *int) error {
	var park, retries any
	if maxParkDurationSeconds != nil {
		park = *maxParkDurationSeconds
	}
	if maxRetriesWithoutProgress != nil {
		retries = *maxRetriesWithoutProgress
	}
	_, err := q.q(tx).ExecContext(ctx,
		`UPDATE rimsky_node_runs
		    SET max_park_duration_seconds = ?,
		        max_retries_without_progress = ?
		  WHERE id = ?`,
		park, retries, dispatchID.String(),
	)
	if err != nil {
		return fmt.Errorf("sqlite.UpdateDispatchTuningInTx: %w", err)
	}
	return nil
}

// @concept: executor
func (q *queueImpl) LoadScratchInTx(ctx context.Context, tx persistence.Tx, dispatchID shared.UUID) ([]byte, string, string, error) {
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
		dispatchID.String(),
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
func (q *queueImpl) WriteScratchInTx(ctx context.Context, tx persistence.Tx, dispatchID shared.UUID, inline []byte, handle, handleBackend string) error {
	if tx == nil {
		return errors.New("sqlite.WriteScratchInTx: tx required")
	}
	if len(inline) > 0 && handle != "" {
		return errors.New("sqlite.WriteScratchInTx: inline and handle are mutually exclusive")
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
		inlineArg, nullableString(handle), nullableString(handleBackend), dispatchID.String(),
	)
	if err != nil {
		return fmt.Errorf("sqlite.WriteScratchInTx: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite.WriteScratchInTx: rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("sqlite.WriteScratchInTx: %s: %w", dispatchID, persistence.ErrRunRowMissing)
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

type rowScanner interface {
	Scan(dest ...any) error
}

func scanOneSqliteParkedRow(row rowScanner) (*persistence.ParkedRow, error) {
	var (
		idStr, nodeIDStr   string
		executor           sql.NullString
		storesStr          string
		frameIDStr         string
		parkedAtStr        string
		resumeAtStr        sql.NullString
		reason             sql.NullString
		reasonNote         sql.NullString
		maxParkSec         sql.NullInt64
		consecutiveRetries int
	)
	if err := row.Scan(
		&idStr, &nodeIDStr, &executor, &storesStr, &frameIDStr,
		&parkedAtStr, &resumeAtStr, &reason, &reasonNote,
		&maxParkSec, &consecutiveRetries,
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
		DispatchID:               disp,
		NodeID:                   node,
		FrameID:                  frame,
		ParkedAt:                 parkedAt,
		ConsecutiveRetriesNoProg: consecutiveRetries,
		RequiredStores:           stores,
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
	if reason.Valid {
		out.Reason = reason.String
	}
	if reasonNote.Valid {
		out.ReasonNote = reasonNote.String
	}
	if maxParkSec.Valid {
		v := int(maxParkSec.Int64)
		out.MaxParkDurationSeconds = &v
	}
	return out, nil
}
