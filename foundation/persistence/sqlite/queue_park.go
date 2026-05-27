// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// queue_park.go is the SQLite impl of the parked-row helpers added to
// persistence.Queue by the 2026-05-08 platform-extensions plan (sections
// E1, E3, E4, E5). Mirrors postgres/queue_park.go method-for-method.

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/foundation/shared"
)

// ParkActiveInTx transitions a node-run row from phase='active' to
// phase='parked' under the supplied claimant guard.
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
	var payloadInline any
	if len(in.PayloadInline) > 0 {
		payloadInline = in.PayloadInline
	}
	res, err := q.q(tx).ExecContext(ctx,
		`UPDATE rimsky_node_runs
		    SET phase = 'parked',
		        claimed_by = NULL,
		        claimed_at = NULL,
		        last_heartbeat_at = NULL,
		        parked_at = ?,
		        resume_at = ?,
		        parked_reason = ?,
		        parked_reason_note = ?,
		        parked_reason_label = ?,
		        session_token = ?,
		        parked_payload_inline = ?,
		        parked_payload_handle = ?,
		        parked_payload_handle_backend = ?
		  WHERE id = ?
		    AND claimed_by = ?
		    AND phase = 'active'`,
		formatTime(in.ParkedAt), resumeAt,
		nullableString(in.Reason), nullableString(in.ReasonNote),
		nullableString(in.ReasonLabel),
		nullableString(in.SessionToken),
		payloadInline,
		nullableString(in.PayloadHandle), nullableString(in.PayloadHandleBackend),
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

// ListParkedReadyForResume returns up to limit parked rows whose resume_at
// has elapsed (resume_at <= cutoff).
func (q *queueImpl) ListParkedReadyForResume(ctx context.Context, cutoff time.Time, limit int) ([]persistence.ParkedRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := q.db.QueryContext(ctx,
		`SELECT d.id, d.node_id, d.executor_name, d.required_stores, d.frame_id,
		        d.parked_at, d.resume_at, d.parked_reason, d.parked_reason_note, d.session_token,
		        d.parked_payload_inline, d.parked_payload_handle, d.parked_payload_handle_backend,
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

// ListParkedOverdue returns parked rows whose parked_at +
// max_park_duration_seconds is older than now. SQLite has no native
// `interval`; we compare via app-side filter on the loaded rows.
func (q *queueImpl) ListParkedOverdue(ctx context.Context, now time.Time, limit int) ([]persistence.ParkedRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := q.db.QueryContext(ctx,
		`SELECT d.id, d.node_id, d.executor_name, d.required_stores, d.frame_id,
		        d.parked_at, d.resume_at, d.parked_reason, d.parked_reason_note, d.session_token,
		        d.parked_payload_inline, d.parked_payload_handle, d.parked_payload_handle_backend,
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
	// App-side filter: parked_at + max_park_duration_seconds <= now,
	// AND resume_at is either NULL or strictly in the future. The
	// resume_at predicate prevents racing the deadline-elapsed wake
	// path (which picks rows whose resume_at <= now) — see the postgres
	// mirror for the full rationale.
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
			// resume_at has elapsed → row is the wake path's, skip.
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// ListParkedDiagnostic returns currently-parked rows for the admin
// diagnostic endpoints. Joins rimsky_nodes for the instance_id needed
// by the endpoints' frame/instance grouping.
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

// GetParkedByNode returns the parked row for a node, or nil.
//
// When `runID` is non-nil the SELECT is narrowed to that specific
// in-flight row — fan-out children share a node_id with their siblings,
// so a SELECT by node_id alone can return any of the in-flight parked
// rows while children race. Nil `runID` preserves the legacy by-node
// lookup for paths that don't face fan-out ambiguity.
//
// @concept: fan-out
func (q *queueImpl) GetParkedByNode(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID) (*persistence.ParkedRow, error) {
	row := q.db.QueryRowContext(ctx,
		`SELECT d.id, d.node_id, d.executor_name, d.required_stores, d.frame_id,
		        d.parked_at, d.resume_at, d.parked_reason, d.parked_reason_note, d.session_token,
		        d.parked_payload_inline, d.parked_payload_handle, d.parked_payload_handle_backend,
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

// ResumeParkedInTx transitions parked→pending so the next
// SelectCandidates tick picks the row up. Park metadata is preserved
// for the resume-dispatch path. The row goes back to claimed_by=NULL
// so any eligible supervisor can pick it up; the wake-source supervisor
// id is recorded by callers in the parked_resume_started audit event.
// The wakeReason is persisted on rimsky_node_runs.wake_reason so
// the resume-dispatch path's LoadResumeMetadataInTx can attach it to
// ResumeContext.resume_reason. enqueued_at is preserved across the
// resume — see the postgres mirror for the rationale.
func (q *queueImpl) ResumeParkedInTx(ctx context.Context, tx persistence.Tx, dispatchID shared.UUID, wakeReason string) (bool, error) {
	if tx == nil {
		return false, errors.New("sqlite.ResumeParkedInTx: tx required")
	}
	var wakeReasonArg any
	if wakeReason != "" {
		wakeReasonArg = wakeReason
	}
	res, err := q.q(tx).ExecContext(ctx,
		`UPDATE rimsky_node_runs
		    SET phase = 'pending',
		        claimed_by = NULL,
		        claimed_at = NULL,
		        last_heartbeat_at = NULL,
		        resume_at = NULL,
		        wake_reason = ?
		  WHERE id = ?
		    AND phase = 'parked'`,
		wakeReasonArg, dispatchID.String(),
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

// RebindRunFrameInTx updates rimsky_node_runs.frame_id for the given
// dispatch row to `newFrameID`. Mirror of the postgres helper used by
// the hard-dep cascade extension and the standard cascade-subscription
// path to rebind a woken parked run into the active frame.
//
// Returns `persistence.ErrRunRowMissing` when no row matches
// `dispatchID`: callers always reach this primitive after resolving
// the run row, so a silent no-op would hide programmer errors.
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

// GetRetryNoProgress returns counter + per-row override.
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

// SetRetryNoProgressForNodeInTx mirrors the postgres impl: writes the
// carry-forward retry counter onto the current node-run row keyed
// by node_id (used by the retry round-trip to accumulate the counter
// across the remove-and-reinsert cycle).
//
// Scoped to `phase = 'pending' AND claimed_by IS NULL` so only the
// freshly-inserted pending row is touched — fan-out siblings still
// mid-flight in other phases keep their counters intact.
//
// @concept: fan-out
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

// UpdateDispatchTuningInTx writes the per-row dispatch tuning columns.
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

// LoadResumeMetadataInTx returns the parked metadata that survived
// parked → pending so resume dispatch can attach it to ResumeContext.
//
// SQLite stores TIMESTAMP columns as TEXT (RFC3339Nano). modernc/sqlite
// v1.50.0 onward refuses to scan a TEXT column into sql.NullTime —
// `unsupported Scan, storing driver.Value type string into type
// *time.Time`. Per the convention used elsewhere in this file
// (scanOneSqliteParkedRow's resumeAtStr handling) we scan the column
// into sql.NullString and run it through parseTime when valid.
func (q *queueImpl) LoadResumeMetadataInTx(ctx context.Context, tx persistence.Tx, dispatchID shared.UUID) (*persistence.ResumeMetadataRow, error) {
	if tx == nil {
		return nil, errors.New("sqlite.LoadResumeMetadataInTx: tx required")
	}
	var (
		inline      []byte
		handle      sql.NullString
		backend     sql.NullString
		reason      sql.NullString
		reasonNote  sql.NullString
		session     sql.NullString
		wakeReason  sql.NullString
		parkedAtStr sql.NullString
	)
	err := q.q(tx).QueryRowContext(ctx,
		`SELECT parked_payload_inline, parked_payload_handle, parked_payload_handle_backend,
		        parked_reason, parked_reason_note, session_token, wake_reason, parked_at
		   FROM rimsky_node_runs
		  WHERE id = ?`,
		dispatchID.String(),
	).Scan(&inline, &handle, &backend, &reason, &reasonNote, &session, &wakeReason, &parkedAtStr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("sqlite.LoadResumeMetadataInTx: %w", err)
	}
	if len(inline) == 0 && !handle.Valid && !backend.Valid && !reason.Valid && !reasonNote.Valid && !session.Valid && !wakeReason.Valid && !parkedAtStr.Valid {
		return nil, nil
	}
	out := &persistence.ResumeMetadataRow{PayloadInline: inline}
	if handle.Valid {
		out.PayloadHandle = handle.String
	}
	if backend.Valid {
		out.PayloadHandleBackend = backend.String
	}
	if reason.Valid {
		out.Reason = reason.String
	}
	if reasonNote.Valid {
		out.ReasonNote = reasonNote.String
	}
	if session.Valid {
		out.SessionToken = session.String
	}
	if wakeReason.Valid {
		out.WakeReason = wakeReason.String
	}
	if parkedAtStr.Valid {
		t, err := parseTime(parkedAtStr.String)
		if err != nil {
			return nil, fmt.Errorf("sqlite.LoadResumeMetadataInTx: parse parked_at: %w", err)
		}
		out.ParkedAt = t
	}
	return out, nil
}

// ClearResumeMetadataInTx clears the parked_* metadata after a
// successful resume.
func (q *queueImpl) ClearResumeMetadataInTx(ctx context.Context, tx persistence.Tx, dispatchID shared.UUID) error {
	if tx == nil {
		return errors.New("sqlite.ClearResumeMetadataInTx: tx required")
	}
	_, err := q.q(tx).ExecContext(ctx,
		`UPDATE rimsky_node_runs
		    SET parked_at = NULL,
		        parked_reason = NULL,
		        parked_reason_note = NULL,
		        parked_payload_inline = NULL,
		        parked_payload_handle = NULL,
		        parked_payload_handle_backend = NULL,
		        session_token = NULL,
		        wake_reason = NULL
		  WHERE id = ?`,
		dispatchID.String(),
	)
	if err != nil {
		return fmt.Errorf("sqlite.ClearResumeMetadataInTx: %w", err)
	}
	return nil
}

// scanSqliteParkedRows iterates the cursor.
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

// rowScanner abstracts *sql.Row and *sql.Rows so scanOneSqliteParkedRow
// can serve both QueryRow and iteration paths.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanOneSqliteParkedRow(row rowScanner) (*persistence.ParkedRow, error) {
	var (
		idStr, nodeIDStr       string
		executor               sql.NullString
		storesStr              string
		frameIDStr             string
		parkedAtStr            string
		resumeAtStr            sql.NullString
		reason, sessionToken   sql.NullString
		reasonNote             sql.NullString
		payloadInline          []byte
		payloadHandle, backend sql.NullString
		maxParkSec             sql.NullInt64
		consecutiveRetries     int
	)
	if err := row.Scan(
		&idStr, &nodeIDStr, &executor, &storesStr, &frameIDStr,
		&parkedAtStr, &resumeAtStr, &reason, &reasonNote, &sessionToken,
		&payloadInline, &payloadHandle, &backend,
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
		PayloadInline:            payloadInline,
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
	if sessionToken.Valid {
		out.SessionToken = sessionToken.String
	}
	if payloadHandle.Valid {
		out.PayloadHandle = payloadHandle.String
	}
	if backend.Valid {
		out.PayloadHandleBackend = backend.String
	}
	if maxParkSec.Valid {
		v := int(maxParkSec.Int64)
		out.MaxParkDurationSeconds = &v
	}
	return out, nil
}
