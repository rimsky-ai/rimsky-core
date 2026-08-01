// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

const defaultCandidateLimit = 100

type queueImpl struct {
	db     *sql.DB
	tables *tablesImpl
}

func newQueue(db *sql.DB) *queueImpl { return &queueImpl{db: db} }

func (q *queueImpl) setTables(t *tablesImpl) { q.tables = t }

var _ persistence.Queue = (*queueImpl)(nil)

func (q *queueImpl) q(tx persistence.Tx) querier {
	if tx == nil {
		return q.db
	}
	t, ok := tx.(*sqliteTx)
	if !ok {
		panic(fmt.Sprintf("sqlite.queueImpl.q: persistence.Tx is not a sqlite tx: %T", tx))
	}
	return t.tx
}

// @concept: run-scope
// @decision: non-cascade-direct-to-stale
func (q *queueImpl) Enqueue(ctx context.Context, req persistence.DispatchRequest, tx persistence.Tx) error {
	if tx == nil {
		if q.tables == nil {
			return fmt.Errorf("sqlite.Enqueue: queue not wired with tables")
		}
		return q.tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return q.Enqueue(ctx, req, tx)
		})
	}
	claimProducers := req.RequiredClaimProducers
	if claimProducers == nil {
		claimProducers = []string{}
	}
	if req.FrameID == (shared.UUID{}) {
		return fmt.Errorf("sqlite.Enqueue: frame_id required for node %s", req.NodeID)
	}
	if req.RunScopeID == (shared.UUID{}) {
		return fmt.Errorf("sqlite.Enqueue: run_scope_id required for node %s", req.NodeID)
	}
	if q.tables == nil {
		return fmt.Errorf("sqlite.Enqueue: queue not wired with tables (cannot snapshot dispatch_input_bag)")
	}
	var priorNodeRunID any
	if req.PriorNodeRunID != nil {
		priorNodeRunID = req.PriorNodeRunID.String()
	}
	priorDisposition := nullableString(req.PriorDispatchDisposition)
	// @concept: executor
	var scratchInlineArg any
	if len(req.InitialScratchInline) > 0 {
		scratchInlineArg = req.InitialScratchInline
	}
	creationReason := req.CreationReason
	if creationReason == "" {
		creationReason = cascade.CreationReasonCascade
	}
	newRunID := uuid.New()
	res, err := q.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_node_runs (id, node_id, executor_name, required_claim_producers, enqueued_at, state, creation_reason, sequence, frame_id, run_scope_id, prior_dispatch_id, prior_dispatch_disposition, scratch_inline, scratch_handle, scratch_handle_backend)
		 SELECT ?, ?, ?, ?, ?, 'stale', ?,
		        COALESCE((SELECT MAX(sequence) FROM rimsky_node_runs WHERE node_id = ? AND run_scope_id = ?), 0) + 1,
		        ?, rs.id, ?, ?, ?, ?, ?
		   FROM rimsky_run_scopes rs
		  WHERE rs.id = ?
		    AND rs.closed_at IS NULL`,
		newRunID.String(), req.NodeID.String(),
		nullableString(req.ExecutorName), marshalStringArray(claimProducers),
		formatTime(req.EnqueuedAt), string(creationReason),
		req.NodeID.String(), req.RunScopeID.String(),
		req.FrameID.String(),
		priorNodeRunID, priorDisposition,
		scratchInlineArg, nullableString(req.InitialScratchHandle), nullableString(req.InitialScratchHandleBackend),
		req.RunScopeID.String(),
	)
	if err != nil {
		return fmt.Errorf("sqlite.Enqueue: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite.Enqueue: rows affected: %w", err)
	}
	if affected == 0 {
		var closedAt sql.NullString
		err = q.q(tx).QueryRowContext(ctx,
			`SELECT closed_at FROM rimsky_run_scopes WHERE id = ?`,
			req.RunScopeID.String(),
		).Scan(&closedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("sqlite.Enqueue: run scope %s not found", req.RunScopeID)
		}
		if err != nil {
			return fmt.Errorf("sqlite.Enqueue: lookup run scope: %w", err)
		}
		if closedAt.Valid {
			return persistence.ErrRunScopeClosed
		}
		return fmt.Errorf("sqlite.Enqueue: insert returned no rows")
	}
	if err := (*nodeAttributesImpl)(q.tables).SnapshotBagForNewRun(ctx, shared.UUID(newRunID), req.NodeID, req.RunScopeID, tx); err != nil {
		return fmt.Errorf("sqlite.Enqueue: snapshot bag: %w", err)
	}
	return nil
}

func (q *queueImpl) SelectCandidates(
	ctx context.Context, req persistence.SelectCandidatesRequest, tx persistence.Tx,
) ([]persistence.Candidate, error) {
	if tx == nil {
		return nil, errors.New("sqlite.SelectCandidates: tx required")
	}
	if _, err := unwrapTx(tx); err != nil {
		return nil, fmt.Errorf("sqlite.SelectCandidates: %w", err)
	}
	limit := req.Limit
	if limit <= 0 {
		limit = defaultCandidateLimit
	}

	rows, err := q.q(tx).QueryContext(ctx,
		`SELECT d.id, d.node_id, n.node_type, d.executor_name, d.required_claim_producers, d.enqueued_at, d.frame_id,
		        d.prior_dispatch_id, d.prior_dispatch_disposition
		   FROM rimsky_node_runs d
		   JOIN rimsky_nodes n ON n.id = d.node_id
		   JOIN rimsky_instances i ON i.id = n.instance_id
		  WHERE d.claimed_by IS NULL
		    AND d.state = 'stale'
		    AND i.paused = 0
		    AND i.terminated_at IS NULL
		    AND d.enqueued_at <= ?
		    AND NOT EXISTS (
		      SELECT 1 FROM rimsky_node_runs other
		       WHERE other.node_id = d.node_id
		         AND other.run_scope_id = d.run_scope_id
		         AND other.id <> d.id
		         AND (other.claimed_by IS NOT NULL OR other.state IN ('held','parked'))
		    )
		    AND NOT EXISTS (
		      SELECT 1 FROM rimsky_wait_set w
		      WHERE w.frame_id = d.frame_id AND w.receiver_run_id = d.id
		        AND w.drained_at IS NULL
		    )
		  ORDER BY d.enqueued_at, d.id`,
		nowUTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite.SelectCandidates: %w", err)
	}
	defer rows.Close()

	var out []persistence.Candidate
	for rows.Next() {
		var (
			c                         persistence.Candidate
			dispatchIDStr             string
			nodeIDStr                 string
			nodeType                  string
			executorName              sql.NullString
			requiredClaimProducersStr string
			enqueuedAtStr             string
			frameIDStr                string
			priorDispatchIDStr        sql.NullString
			priorDispositionStr       sql.NullString
		)
		if err := rows.Scan(&dispatchIDStr, &nodeIDStr, &nodeType, &executorName,
			&requiredClaimProducersStr, &enqueuedAtStr, &frameIDStr,
			&priorDispatchIDStr, &priorDispositionStr); err != nil {
			return nil, fmt.Errorf("sqlite.SelectCandidates: scan: %w", err)
		}
		c.NodeType = nodeType
		if executorName.Valid {
			c.ExecutorName = executorName.String
		}
		if priorDispatchIDStr.Valid {
			pid, perr := uuid.Parse(priorDispatchIDStr.String)
			if perr != nil {
				return nil, fmt.Errorf("sqlite.SelectCandidates: parse prior_dispatch_id: %w", perr)
			}
			c.PriorNodeRunID = &pid
		}
		if priorDispositionStr.Valid {
			c.PriorDispatchDisposition = priorDispositionStr.String
		}
		claimProducers, err := unmarshalStringArray(requiredClaimProducersStr)
		if err != nil {
			return nil, err
		}
		c.RequiredClaimProducers = claimProducers
		if c.ExecutorName == "" && len(c.RequiredClaimProducers) == 0 {
			continue
		}
		if c.NodeRunID, err = uuid.Parse(dispatchIDStr); err != nil {
			return nil, err
		}
		if c.NodeID, err = uuid.Parse(nodeIDStr); err != nil {
			return nil, err
		}
		if c.FrameID, err = uuid.Parse(frameIDStr); err != nil {
			return nil, err
		}
		if c.EnqueuedAt, err = parseTime(enqueuedAtStr); err != nil {
			return nil, err
		}
		if !req.CursorEnqueuedAfter.IsZero() {
			if c.EnqueuedAt.Before(req.CursorEnqueuedAfter) {
				continue
			}
			if c.EnqueuedAt.Equal(req.CursorEnqueuedAfter) &&
				bytes.Compare(c.NodeRunID[:], req.CursorAfterNodeRunID[:]) <= 0 {
				continue
			}
		}
		out = append(out, c)
		if len(out) >= limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite.SelectCandidates: rows: %w", err)
	}
	return out, nil
}

func (q *queueImpl) ClaimDispatchRow(
	ctx context.Context, nodeRunID shared.UUID, supervisorID string, tx persistence.Tx,
) (bool, error) {
	if tx == nil {
		return false, errors.New("sqlite.ClaimDispatchRow: tx required")
	}
	now := nowUTC()
	res, err := q.q(tx).ExecContext(ctx,
		`UPDATE rimsky_node_runs
		    SET claimed_by = ?, claimed_at = ?, last_progress_at = ?
		  WHERE id = ? AND (claimed_by IS NULL OR claimed_by = ?) AND state = 'stale'
		    AND NOT EXISTS (
		      SELECT 1 FROM rimsky_node_runs other
		       WHERE other.node_id = rimsky_node_runs.node_id
		         AND other.run_scope_id = rimsky_node_runs.run_scope_id
		         AND other.id <> rimsky_node_runs.id
		         AND (other.claimed_by IS NOT NULL OR other.state IN ('held','parked'))
		    )`,
		supervisorID, now, now, nodeRunID.String(), supervisorID,
	)
	if err != nil {
		return false, fmt.Errorf("sqlite.ClaimDispatchRow: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

func (q *queueImpl) PromoteClaimedToRunning(
	ctx context.Context, nodeRunID shared.UUID, supervisorID string, tx persistence.Tx,
) (bool, error) {
	if tx == nil {
		return false, errors.New("sqlite.PromoteClaimedToRunning: tx required")
	}
	now := nowUTC()
	res, err := q.q(tx).ExecContext(ctx,
		`UPDATE rimsky_node_runs
		    SET state = 'running', last_progress_at = ?
		  WHERE id = ? AND claimed_by = ? AND state = 'stale'`,
		now, nodeRunID.String(), supervisorID,
	)
	if err != nil {
		return false, fmt.Errorf("sqlite.PromoteClaimedToRunning: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

func (q *queueImpl) Complete(ctx context.Context, nodeRunID shared.UUID, expectedClaimedBy string) error {
	now := nowUTC()
	_, err := q.db.ExecContext(ctx,
		`UPDATE rimsky_node_runs
		    SET claimed_by = NULL,
		        active_terminal_at = ?
		  WHERE id = ?
		    AND claimed_by = ?`,
		now, nodeRunID.String(), expectedClaimedBy,
	)
	return err
}

func (q *queueImpl) ForceComplete(ctx context.Context, nodeRunID shared.UUID) error {
	now := nowUTC()
	_, err := q.db.ExecContext(ctx,
		`UPDATE rimsky_node_runs
		    SET claimed_by = NULL,
		        active_terminal_at = ?
		  WHERE id = ?`,
		now, nodeRunID.String(),
	)
	return err
}

// @concept: run-scope
func (q *queueImpl) RemoveForNode(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID, expectedClaimedBy string, tx persistence.Tx) error {
	now := nowUTC()
	_, err := q.q(tx).ExecContext(ctx,
		`UPDATE rimsky_node_runs
		    SET claimed_by = NULL,
		        active_terminal_at = ?
		  WHERE node_id = ?
		    AND run_scope_id = ?
		    AND claimed_by = ?`,
		now, nodeID.String(), runScopeID.String(), expectedClaimedBy,
	)
	return err
}

func (q *queueImpl) ForceRemoveForNode(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID, tx persistence.Tx) error {
	now := nowUTC()
	_, err := q.q(tx).ExecContext(ctx,
		`UPDATE rimsky_node_runs
		    SET claimed_by = NULL,
		        active_terminal_at = ?
		  WHERE node_id = ?
		    AND run_scope_id = ?
		    AND claimed_by IS NOT NULL`,
		now, nodeID.String(), runScopeID.String(),
	)
	return err
}

func (q *queueImpl) ListOrphanedClaims(ctx context.Context) ([]persistence.DispatchRow, error) {
	rows, err := q.db.QueryContext(ctx,
		`SELECT id, node_id, state, executor_name, required_claim_producers, enqueued_at,
		        claimed_by, claimed_at, frame_id, async_ack_id,
		        async_ack_registered_at, last_progress_at, tags,
		        effective_max_quiet_period_seconds, effective_max_runtime_seconds,
		        async_ack_principal, async_callback_url
		   FROM rimsky_node_runs
		  WHERE claimed_by IS NOT NULL
		    AND async_ack_id IS NOT NULL`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []persistence.DispatchRow
	for rows.Next() {
		r, err := scanDispatchRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (q *queueImpl) ReleaseClaim(ctx context.Context, nodeRunID shared.UUID, expectedClaimedBy string) error {
	_, err := q.db.ExecContext(ctx,
		`UPDATE rimsky_node_runs
		    SET claimed_by = NULL, claimed_at = NULL, state = 'stale'
		  WHERE id = ? AND claimed_by = ? AND state NOT IN ('fresh', 'failed')`,
		nodeRunID.String(), expectedClaimedBy,
	)
	return err
}

func (q *queueImpl) ForceReleaseClaim(ctx context.Context, nodeRunID shared.UUID) error {
	_, err := q.db.ExecContext(ctx,
		`UPDATE rimsky_node_runs
		    SET claimed_by = NULL, claimed_at = NULL, state = 'stale'
		  WHERE id = ?`,
		nodeRunID.String(),
	)
	return err
}

// @concept: node-run
func (q *queueImpl) ReleaseClaimWithDisposition(ctx context.Context, nodeRunID shared.UUID, expectedClaimedBy string, disposition string) error {
	if disposition == "" {
		return errors.New("sqlite.ReleaseClaimWithDisposition: disposition required")
	}
	_, err := q.db.ExecContext(ctx,
		`UPDATE rimsky_node_runs
		    SET claimed_by = NULL, claimed_at = NULL, state = 'stale',
		        prior_dispatch_id = id, prior_dispatch_disposition = ?
		  WHERE id = ? AND claimed_by = ? AND state NOT IN ('fresh', 'failed')`,
		disposition, nodeRunID.String(), expectedClaimedBy,
	)
	if err != nil {
		return fmt.Errorf("sqlite.ReleaseClaimWithDisposition: %w", err)
	}
	return nil
}

// @concept: node-run
func (q *queueImpl) StampPriorDispatch(ctx context.Context, nodeRunID shared.UUID, priorNodeRunID shared.UUID, disposition string, tx persistence.Tx) error {
	if tx == nil {
		return errors.New("sqlite.StampPriorDispatch: tx required")
	}
	if disposition == "" {
		return errors.New("sqlite.StampPriorDispatch: disposition required")
	}
	res, err := q.q(tx).ExecContext(ctx,
		`UPDATE rimsky_node_runs
		    SET prior_dispatch_id = ?, prior_dispatch_disposition = ?
		  WHERE id = ?`,
		priorNodeRunID.String(), disposition, nodeRunID.String(),
	)
	if err != nil {
		return fmt.Errorf("sqlite.StampPriorDispatch: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite.StampPriorDispatch: rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("sqlite.StampPriorDispatch: %s: %w", nodeRunID, persistence.ErrNotFound)
	}
	return nil
}

func (q *queueImpl) GetDispatchNode(ctx context.Context, nodeRunID shared.UUID, tx persistence.Tx) (shared.UUID, persistence.ClaimOwnership, error) {
	return q.getDispatchNode(ctx, q.q(tx), nodeRunID)
}

func (q *queueImpl) getDispatchNode(ctx context.Context, exec querier, nodeRunID shared.UUID) (shared.UUID, persistence.ClaimOwnership, error) {
	var (
		nodeIDStr string
		claimedBy sql.NullString
	)
	err := exec.QueryRowContext(ctx,
		`SELECT node_id, claimed_by FROM rimsky_node_runs WHERE id = ?`,
		nodeRunID.String(),
	).Scan(&nodeIDStr, &claimedBy)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return shared.UUID{}, persistence.ClaimOwnership{Kind: persistence.ClaimOwnershipKindNotFound}, nil
		}
		return shared.UUID{}, persistence.ClaimOwnership{}, err
	}
	nodeID, perr := uuid.Parse(nodeIDStr)
	if perr != nil {
		return shared.UUID{}, persistence.ClaimOwnership{}, perr
	}
	if !claimedBy.Valid {
		return nodeID, persistence.ClaimOwnership{Kind: persistence.ClaimOwnershipKindUnclaimed}, nil
	}
	return nodeID, persistence.ClaimOwnership{Kind: persistence.ClaimOwnershipKindClaimedBy, SupervisorID: claimedBy.String}, nil
}

func (q *queueImpl) GetClaimedBy(ctx context.Context, nodeRunID shared.UUID) (persistence.ClaimOwnership, error) {
	var claimedBy sql.NullString
	err := q.db.QueryRowContext(ctx,
		`SELECT claimed_by FROM rimsky_node_runs WHERE id = ?`,
		nodeRunID.String(),
	).Scan(&claimedBy)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return persistence.ClaimOwnership{Kind: persistence.ClaimOwnershipKindNotFound}, nil
		}
		return persistence.ClaimOwnership{}, err
	}
	if !claimedBy.Valid {
		return persistence.ClaimOwnership{Kind: persistence.ClaimOwnershipKindUnclaimed}, nil
	}
	return persistence.ClaimOwnership{Kind: persistence.ClaimOwnershipKindClaimedBy, SupervisorID: claimedBy.String}, nil
}

func (q *queueImpl) LookupRunByAsyncAckID(ctx context.Context, ackID string, tx persistence.Tx) (*persistence.DispatchRow, error) {
	if tx == nil {
		return nil, errors.New("sqlite.LookupRunByAsyncAckID: tx required")
	}
	row := q.q(tx).QueryRowContext(ctx,
		`SELECT id, node_id, state, executor_name, required_claim_producers, enqueued_at,
		        claimed_by, claimed_at, frame_id, async_ack_id,
		        async_ack_registered_at, last_progress_at, tags,
		        effective_max_quiet_period_seconds, effective_max_runtime_seconds,
		        async_ack_principal, async_callback_url
		   FROM rimsky_node_runs
		  WHERE async_ack_id = ?`,
		ackID,
	)
	r, err := scanDispatchRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("sqlite.LookupRunByAsyncAckID: %w", err)
	}
	return &r, nil
}

func (q *queueImpl) RegisterAsyncAck(ctx context.Context, runID shared.UUID, ackID string, now time.Time, maxQuietSec *int, maxRuntimeSec *int, expectedPrincipal string, callbackURL string, tx persistence.Tx) error {
	if tx == nil {
		return errors.New("sqlite.RegisterAsyncAck: tx required")
	}
	if ackID == "" {
		return errors.New("sqlite.RegisterAsyncAck: ackID required")
	}
	var maxQuietArg, maxRuntimeArg any
	if maxQuietSec != nil {
		maxQuietArg = *maxQuietSec
	}
	if maxRuntimeSec != nil {
		maxRuntimeArg = *maxRuntimeSec
	}
	res, err := q.q(tx).ExecContext(ctx,
		`UPDATE rimsky_node_runs
		    SET async_ack_id = ?,
		        async_ack_registered_at = ?,
		        last_progress_at = ?,
		        effective_max_quiet_period_seconds = ?,
		        effective_max_runtime_seconds = ?,
		        async_ack_principal = ?,
		        async_callback_url = ?
		  WHERE id = ?`,
		ackID, formatTime(now), formatTime(now), maxQuietArg, maxRuntimeArg, nullableString(expectedPrincipal),
		nullableString(callbackURL), runID.String(),
	)
	if err != nil {
		return fmt.Errorf("sqlite.RegisterAsyncAck: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite.RegisterAsyncAck: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("sqlite.RegisterAsyncAck: %s: %w", runID, persistence.ErrNotFound)
	}
	return nil
}

func (q *queueImpl) BumpLastProgressAt(ctx context.Context, runID shared.UUID, now time.Time, tx persistence.Tx) (bool, error) {
	res, err := q.q(tx).ExecContext(ctx,
		`UPDATE rimsky_node_runs SET last_progress_at = ? WHERE id = ?`,
		formatTime(now), runID.String(),
	)
	if err != nil {
		return false, fmt.Errorf("sqlite.BumpLastProgressAt: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("sqlite.BumpLastProgressAt: rows-affected: %w", err)
	}
	return n > 0, nil
}

func (q *queueImpl) ListLive(ctx context.Context, filter persistence.DispatchListFilter, pag persistence.ListPagination) (persistence.PaginatedListResult[persistence.DispatchRow], error) {
	limit := pag.Limit
	if limit <= 0 {
		limit = 50
	}
	stateClause, executor, instanceID := buildLiveDispatchFilters(filter)
	args := []any{}
	args = append(args, executor, executor)
	args = append(args, instanceID, instanceID)
	cursorClause := ""
	if pag.Cursor != "" {
		oc, id, err := decodeDispatchCursor(pag.Cursor)
		if err != nil {
			return persistence.PaginatedListResult[persistence.DispatchRow]{}, fmt.Errorf("sqlite.ListLive: bad cursor: %w", err)
		}
		cursorClause = " AND (d.enqueued_at, d.id) < (?, ?)"
		args = append(args, formatTime(oc), id.String())
	}
	args = append(args, limit)
	q1 := `SELECT d.id, d.node_id, d.state, d.executor_name, d.required_claim_producers, d.enqueued_at,
	        d.claimed_by, d.claimed_at, d.frame_id, d.async_ack_id,
	        d.async_ack_registered_at, d.last_progress_at, d.tags,
	        d.effective_max_quiet_period_seconds, d.effective_max_runtime_seconds,
	        d.async_ack_principal, d.async_callback_url
	   FROM rimsky_node_runs d
	   LEFT JOIN rimsky_nodes n ON n.id = d.node_id
	  WHERE d.state IN (` + inFlightNodeRunStates + `)` +
		stateClause +
		` AND (? IS NULL OR d.executor_name = ?)
	    AND (? IS NULL OR n.instance_id = ?)` +
		cursorClause +
		` ORDER BY d.enqueued_at DESC, d.id DESC
	  LIMIT ?`
	rows, err := q.db.QueryContext(ctx, q1, args...)
	if err != nil {
		return persistence.PaginatedListResult[persistence.DispatchRow]{}, err
	}
	defer rows.Close()
	var out []persistence.DispatchRow
	for rows.Next() {
		row, err := scanDispatchRow(rows)
		if err != nil {
			return persistence.PaginatedListResult[persistence.DispatchRow]{}, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return persistence.PaginatedListResult[persistence.DispatchRow]{}, err
	}
	var nextCursor string
	if len(out) == limit && len(out) > 0 {
		last := out[len(out)-1]
		nextCursor = encodeDispatchCursor(last.EnqueuedAt, last.ID)
	}
	return persistence.PaginatedListResult[persistence.DispatchRow]{Rows: out, NextCursor: nextCursor}, nil
}

func (q *queueImpl) CountLive(ctx context.Context, filter persistence.DispatchListFilter) (int, error) {
	stateClause, executor, instanceID := buildLiveDispatchFilters(filter)
	q1 := `SELECT COUNT(*)
	   FROM rimsky_node_runs d
	   LEFT JOIN rimsky_nodes n ON n.id = d.node_id
	  WHERE d.state IN (` + inFlightNodeRunStates + `)` +
		stateClause +
		` AND (? IS NULL OR d.executor_name = ?)
	    AND (? IS NULL OR n.instance_id = ?)`
	var n int
	err := q.db.QueryRowContext(ctx, q1, executor, executor, instanceID, instanceID).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

func (q *queueImpl) CountParked(ctx context.Context) (int, error) {
	var n int
	err := q.db.QueryRowContext(ctx,
		`SELECT COUNT(*)
		   FROM rimsky_node_runs
		  WHERE state = 'parked'`).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

func (q *queueImpl) GetByID(ctx context.Context, id shared.UUID) (*persistence.DispatchRow, error) {
	row := q.db.QueryRowContext(ctx,
		`SELECT d.id, d.node_id, d.state, d.executor_name, d.required_claim_producers, d.enqueued_at,
		        d.claimed_by, d.claimed_at, d.frame_id, d.async_ack_id,
		        d.async_ack_registered_at, d.last_progress_at, d.tags,
	        d.effective_max_quiet_period_seconds, d.effective_max_runtime_seconds,
	        d.async_ack_principal, d.async_callback_url
		   FROM rimsky_node_runs d
		  WHERE d.id = ?
		    AND d.state IN (`+inFlightNodeRunStates+`)`, id.String(),
	)
	r, err := scanDispatchRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

// @concept: run-scope
func (q *queueImpl) GetInFlightRunForNode(ctx context.Context, nodeID, runScopeID shared.UUID, tx persistence.Tx) (shared.UUID, bool, error) {
	row := q.q(tx).QueryRowContext(ctx,
		`SELECT id FROM rimsky_node_runs
		  WHERE node_id = ? AND run_scope_id = ?
		    AND state IN (`+inFlightNodeRunStates+`)
		  ORDER BY sequence ASC
		  LIMIT 1`,
		nodeID.String(), runScopeID.String())
	var idStr string
	if err := row.Scan(&idStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return shared.UUID{}, false, nil
		}
		return shared.UUID{}, false, fmt.Errorf("sqlite.GetInFlightRunForNode: %w", err)
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return shared.UUID{}, false, fmt.Errorf("sqlite.GetInFlightRunForNode: parse %q: %w", idStr, err)
	}
	return id, true, nil
}

// @concept: run-scope
func (q *queueImpl) GetMostRecentRunForNodeInScope(ctx context.Context, nodeID, runScopeID shared.UUID, tx persistence.Tx) (shared.UUID, bool, error) {
	row := q.q(tx).QueryRowContext(ctx,
		`SELECT id FROM rimsky_node_runs
		  WHERE node_id = ? AND run_scope_id = ?
		  ORDER BY sequence DESC
		  LIMIT 1`,
		nodeID.String(), runScopeID.String())
	var idStr string
	if err := row.Scan(&idStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return shared.UUID{}, false, nil
		}
		return shared.UUID{}, false, fmt.Errorf("sqlite.GetMostRecentRunForNodeInScope: %w", err)
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return shared.UUID{}, false, fmt.Errorf("sqlite.GetMostRecentRunForNodeInScope: parse %q: %w", idStr, err)
	}
	return id, true, nil
}

// @concept: wait-set
// @concept: cascade
func (q *queueImpl) ListInFlightRunStates(
	ctx context.Context, nodeIDs []shared.UUID, frameID, runScopeID shared.UUID, tx persistence.Tx,
) (map[shared.UUID][]string, error) {
	out := map[shared.UUID][]string{}
	if len(nodeIDs) == 0 {
		return out, nil
	}
	if tx == nil {
		return nil, errors.New("sqlite.ListInFlightRunStates: tx required")
	}
	placeholders := make([]string, len(nodeIDs))
	args := make([]any, 0, len(nodeIDs)+2)
	for i, id := range nodeIDs {
		placeholders[i] = "?"
		args = append(args, id.String())
	}
	args = append(args, frameID.String(), runScopeID.String())
	rows, err := q.q(tx).QueryContext(ctx,
		`SELECT DISTINCT node_id, state FROM rimsky_node_runs
		  WHERE node_id IN (`+strings.Join(placeholders, ",")+`)
		    AND frame_id = ?
		    AND run_scope_id = ?
		    AND state IN (`+inFlightNodeRunStates+`)`,
		args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite.ListInFlightRunStates: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var nodeIDStr, state string
		if err := rows.Scan(&nodeIDStr, &state); err != nil {
			return nil, fmt.Errorf("sqlite.ListInFlightRunStates: scan: %w", err)
		}
		nodeID, err := uuid.Parse(nodeIDStr)
		if err != nil {
			return nil, fmt.Errorf("sqlite.ListInFlightRunStates: parse node_id: %w", err)
		}
		out[shared.UUID(nodeID)] = append(out[shared.UUID(nodeID)], state)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite.ListInFlightRunStates: rows: %w", err)
	}
	return out, nil
}

func buildLiveDispatchFilters(filter persistence.DispatchListFilter) (stateClause string, executor any, instanceID any) {
	switch filter.State {
	case "pending":
		stateClause = " AND d.claimed_by IS NULL"
	case "claimed":
		stateClause = " AND d.claimed_by IS NOT NULL"
	}
	if filter.ExecutorName != "" {
		executor = filter.ExecutorName
	}
	if filter.InstanceID != nil {
		instanceID = filter.InstanceID.String()
	}
	return stateClause, executor, instanceID
}

func scanDispatchRow(row scannable) (persistence.DispatchRow, error) {
	var (
		idStr                     string
		nodeIDStr                 string
		stateStr                  string
		executorName              sql.NullString
		requiredClaimProducersStr string
		enqueuedAtStr             string
		claimedBy                 sql.NullString
		claimedAtStr              sql.NullString
		frameIDStr                string
		asyncAckID                sql.NullString
		asyncAckRegisteredAt      sql.NullString
		lastProgressAtStr         sql.NullString
		tagsStr                   sql.NullString
		maxQuietSec               sql.NullInt64
		maxRuntimeSec             sql.NullInt64
		asyncAckPrincipal         sql.NullString
		asyncCallbackURL          sql.NullString
		r                         persistence.DispatchRow
	)
	if err := row.Scan(
		&idStr, &nodeIDStr, &stateStr, &executorName, &requiredClaimProducersStr,
		&enqueuedAtStr, &claimedBy, &claimedAtStr, &frameIDStr,
		&asyncAckID, &asyncAckRegisteredAt, &lastProgressAtStr, &tagsStr,
		&maxQuietSec, &maxRuntimeSec, &asyncAckPrincipal, &asyncCallbackURL,
	); err != nil {
		return persistence.DispatchRow{}, err
	}
	r.State = cascade.NodeState(stateStr)
	var err error
	if r.ID, err = uuid.Parse(idStr); err != nil {
		return persistence.DispatchRow{}, err
	}
	if r.NodeID, err = uuid.Parse(nodeIDStr); err != nil {
		return persistence.DispatchRow{}, err
	}
	if executorName.Valid {
		v := executorName.String
		r.ExecutorName = &v
	}
	claimProducers, err := unmarshalStringArray(requiredClaimProducersStr)
	if err != nil {
		return persistence.DispatchRow{}, err
	}
	r.RequiredClaimProducers = claimProducers
	if r.EnqueuedAt, err = parseTime(enqueuedAtStr); err != nil {
		return persistence.DispatchRow{}, err
	}
	if claimedBy.Valid {
		v := claimedBy.String
		r.ClaimedBy = &v
	}
	if claimedAtStr.Valid {
		t, perr := parseTime(claimedAtStr.String)
		if perr != nil {
			return persistence.DispatchRow{}, perr
		}
		r.ClaimedAt = &t
	}
	if r.FrameID, err = uuid.Parse(frameIDStr); err != nil {
		return persistence.DispatchRow{}, err
	}
	if asyncAckID.Valid {
		v := asyncAckID.String
		r.AsyncAckID = &v
	}
	if asyncAckRegisteredAt.Valid {
		t, perr := parseTime(asyncAckRegisteredAt.String)
		if perr != nil {
			return persistence.DispatchRow{}, perr
		}
		r.AsyncAckRegisteredAt = &t
	}
	if lastProgressAtStr.Valid {
		t, perr := parseTime(lastProgressAtStr.String)
		if perr != nil {
			return persistence.DispatchRow{}, perr
		}
		r.LastProgressAt = &t
	}
	rawTags, terr := decodeTagsJSON(tagsStr)
	if terr != nil {
		return persistence.DispatchRow{}, terr
	}
	r.Tags = shared.DedupStrings(rawTags)
	if r.RequiredClaimProducers == nil {
		r.RequiredClaimProducers = []string{}
	}
	if maxQuietSec.Valid {
		v := int(maxQuietSec.Int64)
		r.EffectiveMaxQuietPeriodSeconds = &v
	}
	if maxRuntimeSec.Valid {
		v := int(maxRuntimeSec.Int64)
		r.EffectiveMaxRuntimeSeconds = &v
	}
	if asyncAckPrincipal.Valid {
		v := asyncAckPrincipal.String
		r.AsyncAckPrincipal = &v
	}
	if asyncCallbackURL.Valid {
		v := asyncCallbackURL.String
		r.AsyncCallbackURL = &v
	}
	return r, nil
}

func encodeDispatchCursor(enqueued time.Time, id shared.UUID) string {
	c := struct {
		E time.Time `json:"e"`
		I string    `json:"i"`
	}{E: enqueued, I: id.String()}
	b, _ := json.Marshal(c)
	return base64.StdEncoding.EncodeToString(b)
}

func decodeDispatchCursor(s string) (time.Time, shared.UUID, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, shared.UUID{}, err
	}
	var c struct {
		E time.Time `json:"e"`
		I string    `json:"i"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return time.Time{}, shared.UUID{}, err
	}
	id, err := uuid.Parse(c.I)
	if err != nil {
		return time.Time{}, shared.UUID{}, err
	}
	return c.E, id, nil
}
