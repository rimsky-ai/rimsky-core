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

// ParkActiveInTx transitions a node-run row from phase='active' to
// phase='parked' under the supplied claimant guard. Persists the
// park metadata and clears claimed_by so the orphan-claim reaper
// (`claimed_by IS NOT NULL` predicate) excludes the row.
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
		    SET phase = 'parked',
		        claimed_by = NULL,
		        claimed_at = NULL,
		        parked_at = $3,
		        resume_at = $4,
		        parked_reason = $5,
		        parked_reason_note = NULLIF($6, ''),
		        parked_reason_label = NULLIF($7, '')
		  WHERE id = $1
		    AND claimed_by = $2
		    AND phase = 'active'`,
		in.DispatchID, in.ExpectedClaimedBy, in.ParkedAt, resumeAt,
		in.Reason, in.ReasonNote, in.ReasonLabel,
	)
	if err != nil {
		return fmt.Errorf("postgres.ParkActiveInTx: %w", err)
	}
	if cmd.RowsAffected() != 1 {
		return fmt.Errorf("postgres.ParkActiveInTx: row %s not in expected (active, claimed_by=%s) state", in.DispatchID, in.ExpectedClaimedBy)
	}
	return nil
}

// ListParkedReadyForResume returns up to limit parked rows whose
// resume_at is non-NULL and has elapsed (resume_at <= cutoff).
func (q *queueImpl) ListParkedReadyForResume(ctx context.Context, cutoff time.Time, limit int) ([]persistence.ParkedRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := q.pool.Query(ctx,
		`SELECT d.id, d.node_id, d.executor_name, d.required_stores, d.frame_id,
		        d.parked_at, d.resume_at, d.parked_reason, d.parked_reason_note,
		        d.max_park_duration_seconds, d.consecutive_retries_no_progress
		   FROM rimsky_node_runs d
		  WHERE d.phase = 'parked'
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

// ListParkedOverdue returns parked rows whose parked_at +
// max_park_duration_seconds is older than now AND whose resume_at is
// either NULL or strictly in the future.
func (q *queueImpl) ListParkedOverdue(ctx context.Context, now time.Time, limit int) ([]persistence.ParkedRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := q.pool.Query(ctx,
		`SELECT d.id, d.node_id, d.executor_name, d.required_stores, d.frame_id,
		        d.parked_at, d.resume_at, d.parked_reason, d.parked_reason_note,
		        d.max_park_duration_seconds, d.consecutive_retries_no_progress
		   FROM rimsky_node_runs d
		  WHERE d.phase = 'parked'
		    AND d.max_park_duration_seconds IS NOT NULL
		    AND d.parked_at IS NOT NULL
		    AND d.parked_at + (d.max_park_duration_seconds * INTERVAL '1 second') <= $1
		    AND (d.resume_at IS NULL OR d.resume_at > $1)
		  ORDER BY d.parked_at ASC
		  LIMIT $2`,
		now, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("postgres.ListParkedOverdue: %w", err)
	}
	defer rows.Close()
	return scanParkedRows(rows)
}

// ListParkedDiagnostic returns currently-parked rows for the admin
// diagnostic endpoints.
func (q *queueImpl) ListParkedDiagnostic(ctx context.Context, tx persistence.Tx, reasonFilter string) ([]persistence.ParkedDiagnosticRow, error) {
	if tx == nil {
		return nil, errors.New("postgres.ListParkedDiagnostic: tx required")
	}
	var reasonArg any
	if reasonFilter != "" {
		reasonArg = reasonFilter
	}
	rows, err := q.q(tx).Query(ctx,
		`SELECT d.id, n.instance_id, d.node_id, d.frame_id,
		        d.parked_at, d.resume_at, d.parked_reason, d.parked_reason_note
		   FROM rimsky_node_runs d
		   LEFT JOIN rimsky_nodes n ON n.id = d.node_id
		  WHERE d.phase = 'parked'
		    AND ($1::text IS NULL OR d.parked_reason = $1)
		  ORDER BY d.parked_at ASC`,
		reasonArg,
	)
	if err != nil {
		return nil, fmt.Errorf("postgres.ListParkedDiagnostic: %w", err)
	}
	defer rows.Close()
	var out []persistence.ParkedDiagnosticRow
	for rows.Next() {
		var (
			r          persistence.ParkedDiagnosticRow
			instID     sql.NullString
			frameID    sql.NullString
			resumeAt   sql.NullTime
			reason     sql.NullString
			reasonNote sql.NullString
			nodeID     string
		)
		if err := rows.Scan(&r.DispatchID, &instID, &nodeID, &frameID, &r.ParkedAt, &resumeAt, &reason, &reasonNote); err != nil {
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

// GetParkedByNode returns the parked row for a node, or (nil, nil) when
// the node has no parked node-run row.
func (q *queueImpl) GetParkedByNode(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID) (*persistence.ParkedRow, error) {
	row := q.pool.QueryRow(ctx,
		`SELECT d.id, d.node_id, d.executor_name, d.required_stores, d.frame_id,
		        d.parked_at, d.resume_at, d.parked_reason, d.parked_reason_note,
		        d.max_park_duration_seconds, d.consecutive_retries_no_progress
		   FROM rimsky_node_runs d
		  WHERE d.node_id = $1
		    AND d.run_scope_id = $2
		    AND d.phase = 'parked'`,
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

// ResumeParkedInTx transitions parked→pending so the next
// SelectCandidates tick picks it up via the standard atomic acquisition
// path. The park reason metadata is preserved on the row; resume state
// rides attribute carry-forward per concept:parked-state.
func (q *queueImpl) ResumeParkedInTx(ctx context.Context, tx persistence.Tx, dispatchID shared.UUID) (bool, error) {
	if tx == nil {
		return false, errors.New("postgres.ResumeParkedInTx: tx required")
	}
	cmd, err := q.q(tx).Exec(ctx,
		`UPDATE rimsky_node_runs
		    SET phase = 'pending',
		        claimed_by = NULL,
		        claimed_at = NULL,
		        resume_at = NULL
		  WHERE id = $1
		    AND phase = 'parked'`,
		dispatchID,
	)
	if err != nil {
		return false, fmt.Errorf("postgres.ResumeParkedInTx: %w", err)
	}
	return cmd.RowsAffected() == 1, nil
}

// RebindRunFrameInTx updates rimsky_node_runs.frame_id for the given
// dispatch row to `newFrameID`.
func (q *queueImpl) RebindRunFrameInTx(
	ctx context.Context, tx persistence.Tx,
	dispatchID, newFrameID shared.UUID,
) error {
	if tx == nil {
		return errors.New("postgres.RebindRunFrameInTx: tx required")
	}
	tag, err := q.q(tx).Exec(ctx,
		`UPDATE rimsky_node_runs SET frame_id = $1 WHERE id = $2`,
		newFrameID, dispatchID,
	)
	if err != nil {
		return fmt.Errorf("postgres.RebindRunFrameInTx: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres.RebindRunFrameInTx: %s: %w", dispatchID, persistence.ErrRunRowMissing)
	}
	return nil
}

// GetRetryNoProgress returns the per-row counter and override.
func (q *queueImpl) GetRetryNoProgress(ctx context.Context, dispatchID shared.UUID) (int, *int, error) {
	var (
		count    int
		override sql.NullInt32
	)
	err := q.pool.QueryRow(ctx,
		`SELECT consecutive_retries_no_progress, max_retries_without_progress
		   FROM rimsky_node_runs
		  WHERE id = $1`,
		dispatchID,
	).Scan(&count, &override)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil, nil
		}
		return 0, nil, fmt.Errorf("postgres.GetRetryNoProgress: %w", err)
	}
	if override.Valid {
		v := int(override.Int32)
		return count, &v, nil
	}
	return count, nil, nil
}

// SetRetryNoProgressForNodeInTx writes the carry-forward counter.
func (q *queueImpl) SetRetryNoProgressForNodeInTx(ctx context.Context, tx persistence.Tx, nodeID shared.UUID, runScopeID shared.UUID, count int) error {
	if tx == nil {
		return errors.New("postgres.SetRetryNoProgressForNodeInTx: tx required")
	}
	_, err := q.q(tx).Exec(ctx,
		`UPDATE rimsky_node_runs
		    SET consecutive_retries_no_progress = $3
		  WHERE node_id = $1
		    AND run_scope_id = $2
		    AND phase = 'pending'
		    AND claimed_by IS NULL`,
		nodeID, runScopeID, count,
	)
	if err != nil {
		return fmt.Errorf("postgres.SetRetryNoProgressForNodeInTx: %w", err)
	}
	return nil
}

// UpdateDispatchTuningInTx writes the per-row dispatch tuning columns.
func (q *queueImpl) UpdateDispatchTuningInTx(ctx context.Context, tx persistence.Tx, dispatchID shared.UUID, maxParkDurationSeconds *int, maxRetriesWithoutProgress *int) error {
	_, err := q.q(tx).Exec(ctx,
		`UPDATE rimsky_node_runs
		    SET max_park_duration_seconds = $2,
		        max_retries_without_progress = $3
		  WHERE id = $1`,
		dispatchID, intPtrOrNullPark(maxParkDurationSeconds), intPtrOrNullPark(maxRetriesWithoutProgress),
	)
	if err != nil {
		return fmt.Errorf("postgres.UpdateDispatchTuningInTx: %w", err)
	}
	return nil
}

// scanParkedRows iterates a Rows cursor.
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

// scanOneParkedRow scans either a Rows cursor or a QueryRow result.
func scanOneParkedRow(row pgx.Row) (*persistence.ParkedRow, error) {
	var (
		r          persistence.ParkedRow
		executor   sql.NullString
		stores     []string
		resumeAt   sql.NullTime
		reason     sql.NullString
		reasonNote sql.NullString
		maxParkSec sql.NullInt32
	)
	if err := row.Scan(
		&r.DispatchID, &r.NodeID, &executor, &stores, &r.FrameID,
		&r.ParkedAt, &resumeAt, &reason, &reasonNote,
		&maxParkSec, &r.ConsecutiveRetriesNoProg,
	); err != nil {
		return nil, err
	}
	if executor.Valid {
		r.ExecutorName = executor.String
	}
	if stores == nil {
		stores = []string{}
	}
	r.RequiredStores = stores
	if resumeAt.Valid {
		t := resumeAt.Time
		r.ResumeAt = &t
	}
	if reason.Valid {
		r.Reason = reason.String
	}
	if reasonNote.Valid {
		r.ReasonNote = reasonNote.String
	}
	if maxParkSec.Valid {
		v := int(maxParkSec.Int32)
		r.MaxParkDurationSeconds = &v
	}
	return &r, nil
}

// LoadScratchInTx returns the persisted scratch triple for a dispatch row.
func (q *queueImpl) LoadScratchInTx(ctx context.Context, tx persistence.Tx, dispatchID shared.UUID) ([]byte, string, string, error) {
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
		dispatchID,
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

// WriteScratchInTx persists scratch onto a dispatch row.
func (q *queueImpl) WriteScratchInTx(ctx context.Context, tx persistence.Tx, dispatchID shared.UUID, inline []byte, handle, handleBackend string) error {
	if tx == nil {
		return errors.New("postgres.WriteScratchInTx: tx required")
	}
	if len(inline) > 0 && handle != "" {
		return errors.New("postgres.WriteScratchInTx: inline and handle are mutually exclusive")
	}
	tag, err := q.q(tx).Exec(ctx,
		`UPDATE rimsky_node_runs
		    SET scratch_inline         = $2,
		        scratch_handle         = $3,
		        scratch_handle_backend = $4
		  WHERE id = $1`,
		dispatchID, nilIfEmpty(inline), nilIfEmptyStr(handle), nilIfEmptyStr(handleBackend),
	)
	if err != nil {
		return fmt.Errorf("postgres.WriteScratchInTx: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres.WriteScratchInTx: %s: %w", dispatchID, persistence.ErrRunRowMissing)
	}
	return nil
}

// timeOrNullPark returns the time value or nil for an explicit NULL.
func timeOrNullPark(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

// intPtrOrNullPark returns the int pointer's value or nil for an
// explicit NULL.
func intPtrOrNullPark(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
