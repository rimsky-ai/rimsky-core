// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

const defaultCandidateLimit = 100

type queueImpl struct {
	db *sql.DB
}

func newQueue(db *sql.DB) *queueImpl { return &queueImpl{db: db} }

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

func (q *queueImpl) Enqueue(ctx context.Context, req persistence.DispatchRequest) error {
	return q.inTx(ctx, func(tx persistence.Tx) error {
		return q.EnqueueInTx(ctx, req, tx)
	})
}

func (q *queueImpl) inTx(ctx context.Context, fn func(tx persistence.Tx) error) error {
	sTx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite.queue.inTx: begin: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = sTx.Rollback()
			panic(p)
		}
	}()
	if err := fn(&sqliteTx{tx: sTx}); err != nil {
		_ = sTx.Rollback()
		return err
	}
	return sTx.Commit()
}

// @concept: run-scope
func (q *queueImpl) EnqueueInTx(ctx context.Context, req persistence.DispatchRequest, tx persistence.Tx) error {
	stores := req.RequiredStores
	if stores == nil {
		stores = []string{}
	}
	if req.FrameID == (shared.UUID{}) {
		return fmt.Errorf("sqlite.Enqueue: frame_id required (per blessed-invariant 19) for node %s", req.NodeID)
	}
	if req.RunScopeID == (shared.UUID{}) {
		return fmt.Errorf("sqlite.Enqueue: run_scope_id required for node %s", req.NodeID)
	}
	var priorDispatchID any
	if req.PriorDispatchID != nil {
		priorDispatchID = req.PriorDispatchID.String()
	}
	priorDisposition := nullableString(req.PriorDispatchDisposition)
	// @concept: executor
	var scratchInlineArg any
	if len(req.InitialScratchInline) > 0 {
		scratchInlineArg = req.InitialScratchInline
	}
	res, err := q.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_node_runs (id, node_id, executor_name, required_stores, enqueued_at, phase, frame_id, run_scope_id, prior_dispatch_id, prior_dispatch_disposition, scratch_inline, scratch_handle, scratch_handle_backend)
		 SELECT ?, ?, ?, ?, ?, 'pending', ?, rs.id, ?, ?, ?, ?, ?
		   FROM rimsky_run_scopes rs
		  WHERE rs.id = ?
		    AND rs.closed_at IS NULL
		    AND NOT EXISTS (
		      SELECT 1 FROM rimsky_node_runs
		       WHERE node_id = ?
		         AND run_scope_id = ?
		         AND phase IN ('pending','active','held','parked')
		    )`,
		uuid.New().String(), req.NodeID.String(),
		nullableString(req.ExecutorName), marshalStringArray(stores),
		formatTime(req.EnqueuedAt), req.FrameID.String(),
		priorDispatchID, priorDisposition,
		scratchInlineArg, nullableString(req.InitialScratchHandle), nullableString(req.InitialScratchHandleBackend),
		req.RunScopeID.String(),
		req.NodeID.String(), req.RunScopeID.String(),
	)
	if err != nil {
		return fmt.Errorf("sqlite.Enqueue: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite.Enqueue: rows affected: %w", err)
	}
	if affected > 0 {
		return nil
	}
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
	return nil
}

func (q *queueImpl) SelectCandidates(
	ctx context.Context, tx persistence.Tx, req persistence.SelectCandidatesRequest,
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
	acceptedStores := req.AcceptedStores
	if acceptedStores == nil {
		acceptedStores = []string{}
	}
	acceptedExecutors := req.AcceptedExecutors
	if acceptedExecutors == nil {
		acceptedExecutors = []string{}
	}

	rows, err := q.q(tx).QueryContext(ctx,
		`SELECT d.id, d.node_id, n.node_type, d.executor_name, d.required_stores, d.enqueued_at, d.frame_id,
		        d.prior_dispatch_id, d.prior_dispatch_disposition
		   FROM rimsky_node_runs d
		   JOIN rimsky_nodes n ON n.id = d.node_id
		   JOIN rimsky_instances i ON i.id = n.instance_id
		  WHERE d.claimed_by IS NULL
		    AND d.phase = 'pending'
		    AND i.paused = 0
		    AND d.enqueued_at <= ?
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

	executorAccepted := func(executor string, required []string) bool {
		if executor == "" {
			return len(required) > 0
		}
		for _, a := range acceptedExecutors {
			if a == executor {
				return true
			}
		}
		return false
	}
	storeAccepted := func(required []string) bool {
		for _, r := range required {
			found := false
			for _, a := range acceptedStores {
				if a == r {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}

	var out []persistence.Candidate
	for rows.Next() {
		var (
			c                   persistence.Candidate
			dispatchIDStr       string
			nodeIDStr           string
			nodeType            string
			executorName        sql.NullString
			requiredStoresStr   string
			enqueuedAtStr       string
			frameIDStr          string
			priorDispatchIDStr  sql.NullString
			priorDispositionStr sql.NullString
		)
		if err := rows.Scan(&dispatchIDStr, &nodeIDStr, &nodeType, &executorName,
			&requiredStoresStr, &enqueuedAtStr, &frameIDStr,
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
			c.PriorDispatchID = &pid
		}
		if priorDispositionStr.Valid {
			c.PriorDispatchDisposition = priorDispositionStr.String
		}
		stores, err := unmarshalStringArray(requiredStoresStr)
		if err != nil {
			return nil, err
		}
		c.RequiredStores = stores
		if !executorAccepted(c.ExecutorName, c.RequiredStores) || !storeAccepted(c.RequiredStores) {
			continue
		}
		if c.DispatchID, err = uuid.Parse(dispatchIDStr); err != nil {
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
				bytes.Compare(c.DispatchID[:], req.CursorAfterDispatchID[:]) <= 0 {
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
	ctx context.Context, tx persistence.Tx, dispatchID shared.UUID, supervisorID string,
) (bool, error) {
	if tx == nil {
		return false, errors.New("sqlite.ClaimDispatchRow: tx required")
	}
	now := nowUTC()
	res, err := q.q(tx).ExecContext(ctx,
		`UPDATE rimsky_node_runs
		    SET claimed_by = ?, claimed_at = ?, last_progress_at = ?, phase = 'active'
		  WHERE id = ? AND claimed_by IS NULL AND phase = 'pending'`,
		supervisorID, now, now, dispatchID.String(),
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

func (q *queueImpl) Complete(ctx context.Context, dispatchID shared.UUID, expectedClaimedBy string) error {
	now := nowUTC()
	if expectedClaimedBy != "" {
		_, err := q.db.ExecContext(ctx,
			`UPDATE rimsky_node_runs
			    SET phase = CASE WHEN state = 'failed' THEN 'failed' ELSE 'completed' END,
			        claimed_by = NULL,
			        active_terminal_at = ?
			  WHERE id = ?
			    AND claimed_by = ?
			    AND phase IN ('pending','active','held','parked')`,
			now, dispatchID.String(), expectedClaimedBy,
		)
		return err
	}
	_, err := q.db.ExecContext(ctx,
		`UPDATE rimsky_node_runs
		    SET phase = CASE WHEN state = 'failed' THEN 'failed' ELSE 'completed' END,
		        claimed_by = NULL,
		        active_terminal_at = ?
		  WHERE id = ?
		    AND phase IN ('pending','active','held','parked')`,
		now, dispatchID.String(),
	)
	return err
}

func (q *queueImpl) RemoveForNode(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID, expectedClaimedBy string) error {
	return q.RemoveForNodeInTx(ctx, nodeID, runScopeID, expectedClaimedBy, nil)
}

// @concept: run-scope
func (q *queueImpl) RemoveForNodeInTx(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID, expectedClaimedBy string, tx persistence.Tx) error {
	now := nowUTC()
	if expectedClaimedBy != "" {
		_, err := q.q(tx).ExecContext(ctx,
			`UPDATE rimsky_node_runs
			    SET phase = CASE WHEN state = 'failed' THEN 'failed' ELSE 'completed' END,
			        claimed_by = NULL,
			        active_terminal_at = ?
			  WHERE node_id = ?
			    AND run_scope_id = ?
			    AND claimed_by = ?
			    AND phase IN ('pending','active','held','parked')`,
			now, nodeID.String(), runScopeID.String(), expectedClaimedBy,
		)
		return err
	}
	_, err := q.q(tx).ExecContext(ctx,
		`UPDATE rimsky_node_runs
		    SET phase = CASE WHEN state = 'failed' THEN 'failed' ELSE 'completed' END,
		        claimed_by = NULL,
		        active_terminal_at = ?
		  WHERE node_id = ?
		    AND run_scope_id = ?
		    AND phase IN ('pending','active','held','parked')`,
		now, nodeID.String(), runScopeID.String(),
	)
	return err
}

func (q *queueImpl) ListOrphanedClaims(ctx context.Context) ([]persistence.DispatchRow, error) {
	rows, err := q.db.QueryContext(ctx,
		`SELECT id, node_id, executor_name, required_stores, enqueued_at,
		        claimed_by, claimed_at, frame_id, async_ack_id,
		        async_ack_registered_at, last_progress_at, tags,
		        effective_max_quiet_period_seconds, effective_max_runtime_seconds
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

func (q *queueImpl) ReleaseClaim(ctx context.Context, dispatchID shared.UUID, expectedClaimedBy string) error {
	if expectedClaimedBy != "" {
		_, err := q.db.ExecContext(ctx,
			`UPDATE rimsky_node_runs
			    SET claimed_by = NULL, claimed_at = NULL, phase = 'pending'
			  WHERE id = ? AND claimed_by = ?`,
			dispatchID.String(), expectedClaimedBy,
		)
		return err
	}
	_, err := q.db.ExecContext(ctx,
		`UPDATE rimsky_node_runs
		    SET claimed_by = NULL, claimed_at = NULL, phase = 'pending'
		  WHERE id = ?`,
		dispatchID.String(),
	)
	return err
}

func (q *queueImpl) GetDispatchNode(ctx context.Context, dispatchID shared.UUID) (shared.UUID, persistence.ClaimOwnership, error) {
	var (
		nodeIDStr string
		claimedBy sql.NullString
	)
	err := q.db.QueryRowContext(ctx,
		`SELECT node_id, claimed_by FROM rimsky_node_runs WHERE id = ?`,
		dispatchID.String(),
	).Scan(&nodeIDStr, &claimedBy)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return shared.UUID{}, persistence.ClaimOwnership{Kind: "not_found"}, nil
		}
		return shared.UUID{}, persistence.ClaimOwnership{}, err
	}
	nodeID, perr := uuid.Parse(nodeIDStr)
	if perr != nil {
		return shared.UUID{}, persistence.ClaimOwnership{}, perr
	}
	if !claimedBy.Valid {
		return nodeID, persistence.ClaimOwnership{Kind: "unclaimed"}, nil
	}
	return nodeID, persistence.ClaimOwnership{Kind: "claimed_by", SupervisorID: claimedBy.String}, nil
}

func (q *queueImpl) GetClaimedBy(ctx context.Context, dispatchID shared.UUID) (persistence.ClaimOwnership, error) {
	var claimedBy sql.NullString
	err := q.db.QueryRowContext(ctx,
		`SELECT claimed_by FROM rimsky_node_runs WHERE id = ?`,
		dispatchID.String(),
	).Scan(&claimedBy)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return persistence.ClaimOwnership{Kind: "not_found"}, nil
		}
		return persistence.ClaimOwnership{}, err
	}
	if !claimedBy.Valid {
		return persistence.ClaimOwnership{Kind: "unclaimed"}, nil
	}
	return persistence.ClaimOwnership{Kind: "claimed_by", SupervisorID: claimedBy.String}, nil
}

func (q *queueImpl) LookupRunByAsyncAckID(ctx context.Context, tx persistence.Tx, ackID string) (*persistence.DispatchRow, error) {
	if tx == nil {
		return nil, errors.New("sqlite.LookupRunByAsyncAckID: tx required")
	}
	row := q.q(tx).QueryRowContext(ctx,
		`SELECT id, node_id, executor_name, required_stores, enqueued_at,
		        claimed_by, claimed_at, frame_id, async_ack_id,
		        async_ack_registered_at, last_progress_at, tags,
		        effective_max_quiet_period_seconds, effective_max_runtime_seconds
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

func (q *queueImpl) RegisterAsyncAck(ctx context.Context, tx persistence.Tx, runID shared.UUID, ackID string, now time.Time, maxQuietSec *int, maxRuntimeSec *int) error {
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
		        effective_max_runtime_seconds = ?
		  WHERE id = ?`,
		ackID, formatTime(now), formatTime(now), maxQuietArg, maxRuntimeArg, runID.String(),
	)
	if err != nil {
		return fmt.Errorf("sqlite.RegisterAsyncAck: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite.RegisterAsyncAck: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("sqlite.RegisterAsyncAck: %s: %w", runID, persistence.ErrRunRowMissing)
	}
	return nil
}

func (q *queueImpl) BumpLastProgressAt(ctx context.Context, tx persistence.Tx, runID shared.UUID, now time.Time) (bool, error) {
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
	q1 := `SELECT d.id, d.node_id, d.executor_name, d.required_stores, d.enqueued_at,
	        d.claimed_by, d.claimed_at, d.frame_id, d.async_ack_id,
	        d.async_ack_registered_at, d.last_progress_at, d.tags,
	        d.effective_max_quiet_period_seconds, d.effective_max_runtime_seconds
	   FROM rimsky_node_runs d
	   LEFT JOIN rimsky_nodes n ON n.id = d.node_id
	  WHERE d.phase IN ('pending','active','held','parked')` +
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
	  WHERE d.phase IN ('pending','active','held','parked')` +
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

func (q *queueImpl) CountParkedByReason(ctx context.Context) (map[string]int, error) {
	rows, err := q.db.QueryContext(ctx,
		`SELECT COALESCE(parked_reason, ''), COUNT(*)
		   FROM rimsky_node_runs
		  WHERE phase = 'parked'
		  GROUP BY COALESCE(parked_reason, '')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var reason string
		var n int
		if err := rows.Scan(&reason, &n); err != nil {
			return nil, err
		}
		out[reason] = n
	}
	return out, rows.Err()
}

func (q *queueImpl) GetByID(ctx context.Context, id shared.UUID) (*persistence.DispatchRow, error) {
	row := q.db.QueryRowContext(ctx,
		`SELECT d.id, d.node_id, d.executor_name, d.required_stores, d.enqueued_at,
		        d.claimed_by, d.claimed_at, d.frame_id, d.async_ack_id,
		        d.async_ack_registered_at, d.last_progress_at, d.tags,
	        d.effective_max_quiet_period_seconds, d.effective_max_runtime_seconds
		   FROM rimsky_node_runs d
		  WHERE d.id = ?
		    AND d.phase IN ('pending','active','held','parked')`, id.String(),
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
func (q *queueImpl) GetInFlightRunForNode(ctx context.Context, tx persistence.Tx, nodeID, runScopeID shared.UUID) (shared.UUID, bool, error) {
	row := q.q(tx).QueryRowContext(ctx,
		`SELECT id FROM rimsky_node_runs
		  WHERE node_id = ? AND run_scope_id = ?
		    AND phase IN ('pending','active','held','parked')
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
func (q *queueImpl) GetMostRecentRunForNodeInScope(ctx context.Context, tx persistence.Tx, nodeID, runScopeID shared.UUID) (shared.UUID, bool, error) {
	row := q.q(tx).QueryRowContext(ctx,
		`SELECT id FROM rimsky_node_runs
		  WHERE node_id = ? AND run_scope_id = ?
		  ORDER BY enqueued_at DESC, id DESC
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
func (q *queueImpl) ListInFlightRunPhases(
	ctx context.Context, tx persistence.Tx, nodeIDs []shared.UUID, frameID, runScopeID shared.UUID,
) (map[shared.UUID][]string, error) {
	out := map[shared.UUID][]string{}
	if len(nodeIDs) == 0 {
		return out, nil
	}
	if tx == nil {
		return nil, errors.New("sqlite.ListInFlightRunPhases: tx required")
	}
	placeholders := make([]string, len(nodeIDs))
	args := make([]any, 0, len(nodeIDs)+2)
	for i, id := range nodeIDs {
		placeholders[i] = "?"
		args = append(args, id.String())
	}
	args = append(args, frameID.String(), runScopeID.String())
	rows, err := q.q(tx).QueryContext(ctx,
		`SELECT DISTINCT node_id, phase FROM rimsky_node_runs
		  WHERE node_id IN (`+strings.Join(placeholders, ",")+`)
		    AND frame_id = ?
		    AND run_scope_id = ?
		    AND phase IN ('pending','active','held','parked')`,
		args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite.ListInFlightRunPhases: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var nodeIDStr, phase string
		if err := rows.Scan(&nodeIDStr, &phase); err != nil {
			return nil, fmt.Errorf("sqlite.ListInFlightRunPhases: scan: %w", err)
		}
		nodeID, err := uuid.Parse(nodeIDStr)
		if err != nil {
			return nil, fmt.Errorf("sqlite.ListInFlightRunPhases: parse node_id: %w", err)
		}
		out[shared.UUID(nodeID)] = append(out[shared.UUID(nodeID)], phase)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite.ListInFlightRunPhases: rows: %w", err)
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

type scanner interface {
	Scan(dest ...any) error
}

func scanDispatchRow(row scanner) (persistence.DispatchRow, error) {
	var (
		idStr                string
		nodeIDStr            string
		executorName         sql.NullString
		requiredStoresStr    string
		enqueuedAtStr        string
		claimedBy            sql.NullString
		claimedAtStr         sql.NullString
		frameIDStr           string
		asyncAckID           sql.NullString
		asyncAckRegisteredAt sql.NullString
		lastProgressAtStr    sql.NullString
		tagsStr              sql.NullString
		maxQuietSec          sql.NullInt64
		maxRuntimeSec        sql.NullInt64
		r                    persistence.DispatchRow
	)
	if err := row.Scan(
		&idStr, &nodeIDStr, &executorName, &requiredStoresStr,
		&enqueuedAtStr, &claimedBy, &claimedAtStr, &frameIDStr,
		&asyncAckID, &asyncAckRegisteredAt, &lastProgressAtStr, &tagsStr,
		&maxQuietSec, &maxRuntimeSec,
	); err != nil {
		return persistence.DispatchRow{}, err
	}
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
	stores, err := unmarshalStringArray(requiredStoresStr)
	if err != nil {
		return persistence.DispatchRow{}, err
	}
	r.RequiredStores = stores
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
	r.Tags = dedupTags(rawTags)
	if r.RequiredStores == nil {
		r.RequiredStores = []string{}
	}
	if maxQuietSec.Valid {
		v := int(maxQuietSec.Int64)
		r.EffectiveMaxQuietPeriodSeconds = &v
	}
	if maxRuntimeSec.Valid {
		v := int(maxRuntimeSec.Int64)
		r.EffectiveMaxRuntimeSeconds = &v
	}
	return r, nil
}

func dedupTags(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, t := range in {
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
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
