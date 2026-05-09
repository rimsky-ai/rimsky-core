// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// queue_park.go is the postgres impl of the parked-row helpers added to
// persistence.Queue by the 2026-05-08 platform-extensions plan (sections
// E1, E3, E4, E5).
//
// All transitions involving phase='parked' rows go through this file.
// The ParkActiveInTx helper transitions active→parked under a claimant
// guard (mirrors ReleaseClaim's claimant-guarded UPDATE pattern), the
// ResumeParkedInTx helper transitions parked→active under a fresh
// claimant id, and the per-row counter helpers (GetRetryNoProgress /
// SetRetryNoProgressForNodeInTx) track progress against E5's
// max-retries-without-progress cap.

package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/shared"
)

// ParkActiveInTx transitions a worker_request row from phase='active' to
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
	payloadInline := nilIfEmpty(in.PayloadInline)
	payloadHandle := nilIfEmptyStr(in.PayloadHandle)
	payloadHandleBackend := nilIfEmptyStr(in.PayloadHandleBackend)

	cmd, err := q.q(tx).Exec(ctx,
		`UPDATE rimsky_worker_request
		    SET phase = 'parked',
		        claimed_by = NULL,
		        claimed_at = NULL,
		        last_heartbeat_at = NULL,
		        parked_at = $3,
		        resume_at = $4,
		        parked_reason = $5,
		        session_token = NULLIF($6, ''),
		        parked_payload_inline = $7,
		        parked_payload_handle = $8,
		        parked_payload_handle_backend = $9
		  WHERE id = $1
		    AND claimed_by = $2
		    AND phase = 'active'`,
		in.DispatchID, in.ExpectedClaimedBy, in.ParkedAt, resumeAt,
		in.Reason, in.SessionToken, payloadInline,
		payloadHandle, payloadHandleBackend,
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
		        d.parked_at, d.resume_at, d.parked_reason, d.session_token,
		        d.parked_payload_inline, d.parked_payload_handle, d.parked_payload_handle_backend,
		        d.max_park_duration_seconds, d.consecutive_retries_no_progress
		   FROM rimsky_worker_request d
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
// either NULL or strictly in the future. The latter condition prevents
// the deadline-elapsed wake path and the max_park_duration overrun path
// from racing on the same row: ListParkedReadyForResume picks rows
// whose resume_at <= now, and ListParkedOverdue must skip them so the
// wake transition (parked→pending) and the overdue transition
// (parked→failed) don't fight (the second loses on the state machine's
// "stale → failed under park_timeout" rejection).
func (q *queueImpl) ListParkedOverdue(ctx context.Context, now time.Time, limit int) ([]persistence.ParkedRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := q.pool.Query(ctx,
		`SELECT d.id, d.node_id, d.executor_name, d.required_stores, d.frame_id,
		        d.parked_at, d.resume_at, d.parked_reason, d.session_token,
		        d.parked_payload_inline, d.parked_payload_handle, d.parked_payload_handle_backend,
		        d.max_park_duration_seconds, d.consecutive_retries_no_progress
		   FROM rimsky_worker_request d
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

// GetParkedByNode returns the parked row for a node, or (nil, nil) when
// the node has no parked worker_request row.
func (q *queueImpl) GetParkedByNode(ctx context.Context, nodeID shared.UUID) (*persistence.ParkedRow, error) {
	row := q.pool.QueryRow(ctx,
		`SELECT d.id, d.node_id, d.executor_name, d.required_stores, d.frame_id,
		        d.parked_at, d.resume_at, d.parked_reason, d.session_token,
		        d.parked_payload_inline, d.parked_payload_handle, d.parked_payload_handle_backend,
		        d.max_park_duration_seconds, d.consecutive_retries_no_progress
		   FROM rimsky_worker_request d
		  WHERE d.node_id = $1
		    AND d.phase = 'parked'`,
		nodeID,
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
// path. The park metadata (parked_payload_*, parked_reason,
// session_token) is preserved on the row so the resume-dispatch path
// (E4) can build a ResumeContext from it; the persistence-level
// helpers below (LoadResumeMetadataInTx / ClearResumeMetadataInTx) read
// and clear it after the resume is dispatched.
//
// Note: supervisorID is required by the interface contract but
// intentionally NOT written to claimed_by — we revert to phase='pending'
// / claimed_by=NULL so any eligible supervisor can pick the row up. This
// honors the supervisor-pool specialisation: the supervisor that wakes
// a node may not be the one that runs the resume (e.g. an admin
// invalidate fired against a control-api process that doesn't itself
// run executors). The supervisorID is logged via the caller's audit
// trail (see wakeParkedNode) so the wake-source is still recoverable.
//
// wakeReason is persisted on rimsky_worker_request.wake_reason. The
// resume-dispatch path's LoadResumeMetadataInTx reads it back so the
// executor's ResumeContext.resume_reason matches the actual wake source
// (deadline_elapsed vs external_invalidate). Empty wakeReason persists
// NULL, which the loader maps to "external_invalidate" as a fallback.
//
// enqueued_at is preserved across the resume rather than reset to NOW():
// resumed rows compete with fresh dispatches under the runner's
// `ORDER BY enqueued_at` page, and a row that has been waiting through
// park time should not be deprioritized behind freshly-enqueued rows.
// Operators expect "resume now" not "resume after every fresh dispatch
// in the queue" semantics.
func (q *queueImpl) ResumeParkedInTx(ctx context.Context, tx persistence.Tx, dispatchID shared.UUID, supervisorID, wakeReason string) (bool, error) {
	if tx == nil {
		return false, errors.New("postgres.ResumeParkedInTx: tx required")
	}
	_ = supervisorID
	var wakeReasonArg any
	if wakeReason != "" {
		wakeReasonArg = wakeReason
	}
	cmd, err := q.q(tx).Exec(ctx,
		`UPDATE rimsky_worker_request
		    SET phase = 'pending',
		        claimed_by = NULL,
		        claimed_at = NULL,
		        last_heartbeat_at = NULL,
		        resume_at = NULL,
		        wake_reason = $2
		    -- enqueued_at, parked_at, parked_reason, parked_payload_*,
		    -- session_token are PRESERVED. enqueued_at preservation keeps
		    -- the resumed row from being deprioritized below freshly-
		    -- enqueued rows under ORDER BY enqueued_at. The other park
		    -- metadata is consumed by the resume-dispatch path (E4) to
		    -- build ResumeContext, then cleared via
		    -- ClearResumeMetadataInTx after a successful dispatch.
		  WHERE id = $1
		    AND phase = 'parked'`,
		dispatchID, wakeReasonArg,
	)
	if err != nil {
		return false, fmt.Errorf("postgres.ResumeParkedInTx: %w", err)
	}
	return cmd.RowsAffected() == 1, nil
}

// GetRetryNoProgress returns the per-row counter and override.
func (q *queueImpl) GetRetryNoProgress(ctx context.Context, dispatchID shared.UUID) (int, *int, error) {
	var (
		count    int
		override sql.NullInt32
	)
	err := q.pool.QueryRow(ctx,
		`SELECT consecutive_retries_no_progress, max_retries_without_progress
		   FROM rimsky_worker_request
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

// SetRetryNoProgressForNodeInTx writes the carry-forward counter onto
// the current worker_request row for nodeID. Used by the retry path to
// accumulate the counter across retry round-trips (each retry deletes
// the prior row and inserts a new one with default counter=0).
func (q *queueImpl) SetRetryNoProgressForNodeInTx(ctx context.Context, tx persistence.Tx, nodeID shared.UUID, count int) error {
	if tx == nil {
		return errors.New("postgres.SetRetryNoProgressForNodeInTx: tx required")
	}
	_, err := q.q(tx).Exec(ctx,
		`UPDATE rimsky_worker_request
		    SET consecutive_retries_no_progress = $2
		  WHERE node_id = $1`,
		nodeID, count,
	)
	if err != nil {
		return fmt.Errorf("postgres.SetRetryNoProgressForNodeInTx: %w", err)
	}
	return nil
}

// UpdateDispatchTuningInTx writes the per-row dispatch tuning columns.
func (q *queueImpl) UpdateDispatchTuningInTx(ctx context.Context, tx persistence.Tx, dispatchID shared.UUID, maxParkDurationSeconds *int, maxRetriesWithoutProgress *int) error {
	_, err := q.q(tx).Exec(ctx,
		`UPDATE rimsky_worker_request
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

// LoadResumeMetadataInTx returns the parked metadata that survived the
// parked → pending transition (so the resume dispatch path can attach
// it to ResumeContext on the ExecuteRequest).
func (q *queueImpl) LoadResumeMetadataInTx(ctx context.Context, tx persistence.Tx, dispatchID shared.UUID) (*persistence.ResumeMetadataRow, error) {
	if tx == nil {
		return nil, errors.New("postgres.LoadResumeMetadataInTx: tx required")
	}
	var (
		inline     []byte
		handle     sql.NullString
		backend    sql.NullString
		reason     sql.NullString
		session    sql.NullString
		wakeReason sql.NullString
		parkedAt   sql.NullTime
	)
	err := q.q(tx).QueryRow(ctx,
		`SELECT parked_payload_inline, parked_payload_handle, parked_payload_handle_backend,
		        parked_reason, session_token, wake_reason, parked_at
		   FROM rimsky_worker_request
		  WHERE id = $1`,
		dispatchID,
	).Scan(&inline, &handle, &backend, &reason, &session, &wakeReason, &parkedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("postgres.LoadResumeMetadataInTx: %w", err)
	}
	if len(inline) == 0 && !handle.Valid && !backend.Valid && !reason.Valid && !session.Valid && !wakeReason.Valid && !parkedAt.Valid {
		// No parked metadata → fresh dispatch.
		return nil, nil
	}
	out := &persistence.ResumeMetadataRow{
		PayloadInline: inline,
	}
	if handle.Valid {
		out.PayloadHandle = handle.String
	}
	if backend.Valid {
		out.PayloadHandleBackend = backend.String
	}
	if reason.Valid {
		out.Reason = reason.String
	}
	if session.Valid {
		out.SessionToken = session.String
	}
	if wakeReason.Valid {
		out.WakeReason = wakeReason.String
	}
	if parkedAt.Valid {
		out.ParkedAt = parkedAt.Time
	}
	return out, nil
}

// ClearResumeMetadataInTx clears the parked_payload_* / parked_reason /
// parked_at columns after a successful resume dispatch. session_token
// is also cleared because the executor's resume should consume it once
// (re-park starts a fresh session_token).
func (q *queueImpl) ClearResumeMetadataInTx(ctx context.Context, tx persistence.Tx, dispatchID shared.UUID) error {
	if tx == nil {
		return errors.New("postgres.ClearResumeMetadataInTx: tx required")
	}
	_, err := q.q(tx).Exec(ctx,
		`UPDATE rimsky_worker_request
		    SET parked_at = NULL,
		        parked_reason = NULL,
		        parked_payload_inline = NULL,
		        parked_payload_handle = NULL,
		        parked_payload_handle_backend = NULL,
		        session_token = NULL,
		        wake_reason = NULL
		  WHERE id = $1`,
		dispatchID,
	)
	if err != nil {
		return fmt.Errorf("postgres.ClearResumeMetadataInTx: %w", err)
	}
	return nil
}

// scanParkedRows iterates a Rows cursor and materializes ParkedRow
// values.
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

// scanOneParkedRow scans either a Rows cursor (Next-driven) or a
// QueryRow result. The pgx Row interface satisfies both.
func scanOneParkedRow(row pgx.Row) (*persistence.ParkedRow, error) {
	var (
		r              persistence.ParkedRow
		executor       sql.NullString
		stores         []string
		resumeAt       sql.NullTime
		reason         sql.NullString
		sessionToken   sql.NullString
		payloadInline  []byte
		payloadHandle  sql.NullString
		payloadBackend sql.NullString
		maxParkSec     sql.NullInt32
	)
	if err := row.Scan(
		&r.DispatchID, &r.NodeID, &executor, &stores, &r.FrameID,
		&r.ParkedAt, &resumeAt, &reason, &sessionToken,
		&payloadInline, &payloadHandle, &payloadBackend,
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
	if sessionToken.Valid {
		r.SessionToken = sessionToken.String
	}
	r.PayloadInline = payloadInline
	if payloadHandle.Valid {
		r.PayloadHandle = payloadHandle.String
	}
	if payloadBackend.Valid {
		r.PayloadHandleBackend = payloadBackend.String
	}
	if maxParkSec.Valid {
		v := int(maxParkSec.Int32)
		r.MaxParkDurationSeconds = &v
	}
	return &r, nil
}

// timeOrNullPark returns the time value or nil for an explicit NULL.
// Used by ParkActiveInTx; named distinctly to avoid colliding with the
// pre-existing helpers in events.go and supervisors.go.
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
