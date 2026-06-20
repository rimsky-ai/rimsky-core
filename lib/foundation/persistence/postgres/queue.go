// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

const defaultCandidateLimit = 100

type queueImpl struct {
	pool *pgxpool.Pool
}

func newQueue(pool *pgxpool.Pool) *queueImpl { return &queueImpl{pool: pool} }

var _ persistence.Queue = (*queueImpl)(nil)

func (q *queueImpl) q(tx persistence.Tx) querier {
	if tx == nil {
		return q.pool
	}
	t, ok := tx.(*pgTx)
	if !ok {
		panic(fmt.Sprintf("postgres.queueImpl.q: persistence.Tx is not a postgres tx: %T", tx))
	}
	return t.tx
}

func (q *queueImpl) Enqueue(ctx context.Context, req persistence.DispatchRequest) error {
	return q.EnqueueInTx(ctx, req, nil)
}

// @concept: run-scope
func (q *queueImpl) EnqueueInTx(ctx context.Context, req persistence.DispatchRequest, tx persistence.Tx) error {
	stores := req.RequiredClaimProducers
	if stores == nil {
		stores = []string{}
	}
	executor := nullableText(req.ExecutorName)
	if req.FrameID == (shared.UUID{}) {
		return fmt.Errorf("postgres.Enqueue: frame_id required for node %s", req.NodeID)
	}
	if req.RunScopeID == (shared.UUID{}) {
		return fmt.Errorf("postgres.Enqueue: run_scope_id required for node %s", req.NodeID)
	}
	var priorID any
	if req.PriorDispatchID != nil {
		priorID = *req.PriorDispatchID
	}
	priorDisposition := nullableText(req.PriorDispatchDisposition)
	// @concept: executor
	tag, err := q.q(tx).Exec(ctx,
		`INSERT INTO rimsky_node_runs (id, node_id, executor_name, required_stores, enqueued_at, phase, frame_id, run_scope_id, prior_dispatch_id, prior_dispatch_disposition, scratch_inline, scratch_handle, scratch_handle_backend)
		 SELECT gen_random_uuid(), $1, $2, $3, $4, 'pending', $5, rs.id, $7, $8, $9, $10, $11
		   FROM rimsky_run_scopes rs
		  WHERE rs.id = $6
		    AND rs.closed_at IS NULL
		    AND NOT EXISTS (
		      SELECT 1 FROM rimsky_node_runs
		       WHERE node_id = $1
		         AND run_scope_id = $6
		         AND phase IN ('pending','active','held','parked')
		    )`,
		req.NodeID, executor, stores, req.EnqueuedAt, req.FrameID, req.RunScopeID,
		priorID, priorDisposition,
		nilIfEmpty(req.InitialScratchInline), nilIfEmptyStr(req.InitialScratchHandle), nilIfEmptyStr(req.InitialScratchHandleBackend),
	)
	if err != nil {
		return fmt.Errorf("postgres.Enqueue: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	var closedAt *time.Time
	err = q.q(tx).QueryRow(ctx,
		`SELECT closed_at FROM rimsky_run_scopes WHERE id = $1`,
		req.RunScopeID,
	).Scan(&closedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("postgres.Enqueue: run scope %s not found", req.RunScopeID)
	}
	if err != nil {
		return fmt.Errorf("postgres.Enqueue: lookup run scope: %w", err)
	}
	if closedAt != nil {
		return persistence.ErrRunScopeClosed
	}
	return nil
}

func (q *queueImpl) SelectCandidates(
	ctx context.Context, tx persistence.Tx, req persistence.SelectCandidatesRequest,
) ([]persistence.Candidate, error) {
	if tx == nil {
		return nil, errors.New("postgres.SelectCandidates: tx required")
	}
	pgT, err := unwrapTx(tx)
	if err != nil {
		return nil, fmt.Errorf("postgres.SelectCandidates: %w", err)
	}
	limit := req.Limit
	if limit <= 0 {
		limit = defaultCandidateLimit
	}
	acceptedClaimProducers := req.AcceptedClaimProducers
	if acceptedClaimProducers == nil {
		acceptedClaimProducers = []string{}
	}
	acceptedExecutors := req.AcceptedExecutors
	if acceptedExecutors == nil {
		acceptedExecutors = []string{}
	}

	rows, err := pgT.Query(ctx,
		`SELECT d.id, d.node_id, n.node_type, d.executor_name, d.required_stores, d.enqueued_at, d.frame_id,
		        d.prior_dispatch_id, d.prior_dispatch_disposition, d.state
		   FROM rimsky_node_runs d
		   JOIN rimsky_nodes n ON n.id = d.node_id
		   JOIN rimsky_instances i ON i.id = n.instance_id
		  WHERE d.claimed_by IS NULL
		    AND d.phase = 'pending'
		    AND i.paused = false
		    AND (
		      d.required_stores <@ $1::text[]
		      OR (
		        $5 <> ''
		        AND $5 = ANY($1::text[])
		        AND (SELECT COALESCE(bool_and(i.service_bindings ? rs.name), false) FROM unnest(d.required_stores) AS rs(name))
		      )
		    )
		    AND (
		      d.executor_name = ANY($2::text[])
		      OR (d.executor_name IS NULL AND COALESCE(array_length(d.required_stores, 1), 0) > 0)
		      OR (
		        $4 <> ''
		        AND $4 = ANY($2::text[])
		        AND i.service_bindings ? d.executor_name
		      )
		    )
		    AND d.enqueued_at <= NOW()
		    AND (d.enqueued_at > $6 OR (d.enqueued_at = $6 AND d.id > $7))
		    AND NOT EXISTS (
		      SELECT 1 FROM rimsky_wait_set w
		      WHERE w.frame_id = d.frame_id AND w.receiver_run_id = d.id
		        AND w.drained_at IS NULL
		    )
		  ORDER BY d.enqueued_at, d.id
		  LIMIT $3
		  FOR UPDATE OF d SKIP LOCKED`,
		acceptedClaimProducers, acceptedExecutors, limit, req.LateBindExecutorProxy, req.LateBindClaimProducerProxy,
		req.CursorEnqueuedAfter, req.CursorAfterDispatchID,
	)
	if err != nil {
		return nil, fmt.Errorf("postgres.SelectCandidates: %w", err)
	}
	defer rows.Close()

	var out []persistence.Candidate
	for rows.Next() {
		var (
			c                persistence.Candidate
			executorName     *string
			priorID          *shared.UUID
			priorDisposition *string
			preClaimState    *string
		)
		if err := rows.Scan(
			&c.DispatchID, &c.NodeID, &c.NodeType,
			&executorName, &c.RequiredClaimProducers, &c.EnqueuedAt, &c.FrameID,
			&priorID, &priorDisposition, &preClaimState,
		); err != nil {
			return nil, fmt.Errorf("postgres.SelectCandidates: scan: %w", err)
		}
		if executorName != nil {
			c.ExecutorName = *executorName
		}
		if c.RequiredClaimProducers == nil {
			c.RequiredClaimProducers = []string{}
		}
		c.PriorDispatchID = priorID
		if priorDisposition != nil {
			c.PriorDispatchDisposition = *priorDisposition
		}
		if preClaimState != nil {
			c.PreClaimState = *preClaimState
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres.SelectCandidates: rows: %w", err)
	}
	return out, nil
}

func (q *queueImpl) ClaimDispatchRow(
	ctx context.Context, tx persistence.Tx, dispatchID shared.UUID, supervisorID string,
) (bool, error) {
	if tx == nil {
		return false, errors.New("postgres.ClaimDispatchRow: tx required")
	}
	cmd, err := q.q(tx).Exec(ctx,
		`UPDATE rimsky_node_runs
		    SET claimed_by = $1, claimed_at = NOW(), last_progress_at = NOW(), phase = 'active'
		  WHERE id = $2 AND claimed_by IS NULL AND phase = 'pending'`,
		supervisorID, dispatchID,
	)
	if err != nil {
		return false, fmt.Errorf("postgres.ClaimDispatchRow: %w", err)
	}
	return cmd.RowsAffected() == 1, nil
}

func (q *queueImpl) Complete(ctx context.Context, dispatchID shared.UUID, expectedClaimedBy string) error {
	if expectedClaimedBy != "" {
		_, err := q.pool.Exec(ctx,
			`UPDATE rimsky_node_runs
			    SET phase = CASE WHEN state = 'failed' THEN 'failed' ELSE 'completed' END,
			        claimed_by = NULL,
			        active_terminal_at = NOW()
			  WHERE id = $1
			    AND claimed_by = $2
			    AND phase IN ('pending','active','held','parked')`,
			dispatchID, expectedClaimedBy,
		)
		return err
	}
	_, err := q.pool.Exec(ctx,
		`UPDATE rimsky_node_runs
		    SET phase = CASE WHEN state = 'failed' THEN 'failed' ELSE 'completed' END,
		        claimed_by = NULL,
		        active_terminal_at = NOW()
		  WHERE id = $1
		    AND phase IN ('pending','active','held','parked')`,
		dispatchID,
	)
	return err
}

func (q *queueImpl) RemoveForNode(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID, expectedClaimedBy string) error {
	return q.RemoveForNodeInTx(ctx, nodeID, runScopeID, expectedClaimedBy, nil)
}

// @concept: fan-out
func (q *queueImpl) RemoveForNodeInTx(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID, expectedClaimedBy string, tx persistence.Tx) error {
	if expectedClaimedBy != "" {
		_, err := q.q(tx).Exec(ctx,
			`UPDATE rimsky_node_runs
			    SET phase = CASE WHEN state = 'failed' THEN 'failed' ELSE 'completed' END,
			        claimed_by = NULL,
			        active_terminal_at = NOW()
			  WHERE node_id = $1
			    AND run_scope_id = $2
			    AND claimed_by = $3
			    AND phase IN ('pending','active','held','parked')`,
			nodeID, runScopeID, expectedClaimedBy,
		)
		return err
	}
	_, err := q.q(tx).Exec(ctx,
		`UPDATE rimsky_node_runs
		    SET phase = CASE WHEN state = 'failed' THEN 'failed' ELSE 'completed' END,
		        claimed_by = NULL,
		        active_terminal_at = NOW()
		  WHERE node_id = $1
		    AND run_scope_id = $2
		    AND phase IN ('pending','active','held','parked')`,
		nodeID, runScopeID,
	)
	return err
}

func (q *queueImpl) ListOrphanedClaims(ctx context.Context) ([]persistence.DispatchRow, error) {
	rows, err := q.pool.Query(ctx,
		`SELECT id, node_id, executor_name, required_stores, enqueued_at,
		        claimed_by, claimed_at, frame_id, async_ack_id, async_ack_registered_at,
		        last_progress_at, tags,
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
		var r persistence.DispatchRow
		if err := rows.Scan(
			&r.ID, &r.NodeID, &r.ExecutorName, &r.RequiredClaimProducers,
			&r.EnqueuedAt, &r.ClaimedBy, &r.ClaimedAt, &r.FrameID,
			&r.AsyncAckID, &r.AsyncAckRegisteredAt, &r.LastProgressAt, &r.Tags,
			&r.EffectiveMaxQuietPeriodSeconds, &r.EffectiveMaxRuntimeSeconds,
		); err != nil {
			return nil, err
		}
		if r.RequiredClaimProducers == nil {
			r.RequiredClaimProducers = []string{}
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (q *queueImpl) ReleaseClaim(ctx context.Context, dispatchID shared.UUID, expectedClaimedBy string) error {
	if expectedClaimedBy != "" {
		_, err := q.pool.Exec(ctx,
			`UPDATE rimsky_node_runs
			    SET claimed_by = NULL, claimed_at = NULL, phase = 'pending'
			  WHERE id = $1 AND claimed_by = $2`,
			dispatchID, expectedClaimedBy,
		)
		return err
	}
	_, err := q.pool.Exec(ctx,
		`UPDATE rimsky_node_runs
		    SET claimed_by = NULL, claimed_at = NULL, phase = 'pending'
		  WHERE id = $1`,
		dispatchID,
	)
	return err
}

func (q *queueImpl) GetDispatchNode(ctx context.Context, dispatchID shared.UUID) (shared.UUID, persistence.ClaimOwnership, error) {
	var (
		nodeID    shared.UUID
		claimedBy *string
	)
	err := q.pool.QueryRow(ctx,
		`SELECT node_id, claimed_by FROM rimsky_node_runs WHERE id = $1`,
		dispatchID,
	).Scan(&nodeID, &claimedBy)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return shared.UUID{}, persistence.ClaimOwnership{Kind: "not_found"}, nil
		}
		return shared.UUID{}, persistence.ClaimOwnership{}, err
	}
	if claimedBy == nil {
		return nodeID, persistence.ClaimOwnership{Kind: "unclaimed"}, nil
	}
	return nodeID, persistence.ClaimOwnership{Kind: "claimed_by", SupervisorID: *claimedBy}, nil
}

func (q *queueImpl) GetClaimedBy(ctx context.Context, dispatchID shared.UUID) (persistence.ClaimOwnership, error) {
	var claimedBy *string
	err := q.pool.QueryRow(ctx,
		`SELECT claimed_by FROM rimsky_node_runs WHERE id = $1`,
		dispatchID,
	).Scan(&claimedBy)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return persistence.ClaimOwnership{Kind: "not_found"}, nil
		}
		return persistence.ClaimOwnership{}, err
	}
	if claimedBy == nil {
		return persistence.ClaimOwnership{Kind: "unclaimed"}, nil
	}
	return persistence.ClaimOwnership{Kind: "claimed_by", SupervisorID: *claimedBy}, nil
}

func (q *queueImpl) LookupRunByAsyncAckID(ctx context.Context, tx persistence.Tx, ackID string) (*persistence.DispatchRow, error) {
	if tx == nil {
		return nil, errors.New("postgres.LookupRunByAsyncAckID: tx required")
	}
	row := q.q(tx).QueryRow(ctx,
		`SELECT id, node_id, executor_name, required_stores, enqueued_at,
		        claimed_by, claimed_at, frame_id, async_ack_id, async_ack_registered_at,
		        last_progress_at, tags,
		        effective_max_quiet_period_seconds, effective_max_runtime_seconds
		   FROM rimsky_node_runs
		  WHERE async_ack_id = $1`,
		ackID,
	)
	var r persistence.DispatchRow
	if err := row.Scan(
		&r.ID, &r.NodeID, &r.ExecutorName, &r.RequiredClaimProducers,
		&r.EnqueuedAt, &r.ClaimedBy, &r.ClaimedAt, &r.FrameID,
		&r.AsyncAckID, &r.AsyncAckRegisteredAt, &r.LastProgressAt, &r.Tags,
		&r.EffectiveMaxQuietPeriodSeconds, &r.EffectiveMaxRuntimeSeconds,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("postgres.LookupRunByAsyncAckID: %w", err)
	}
	if r.RequiredClaimProducers == nil {
		r.RequiredClaimProducers = []string{}
	}
	return &r, nil
}

func (q *queueImpl) RegisterAsyncAck(ctx context.Context, tx persistence.Tx, runID shared.UUID, ackID string, now time.Time, maxQuietSec *int, maxRuntimeSec *int) error {
	if tx == nil {
		return errors.New("postgres.RegisterAsyncAck: tx required")
	}
	if ackID == "" {
		return errors.New("postgres.RegisterAsyncAck: ackID required")
	}
	tag, err := q.q(tx).Exec(ctx,
		`UPDATE rimsky_node_runs
		    SET async_ack_id = $2,
		        async_ack_registered_at = $3,
		        last_progress_at = $3,
		        effective_max_quiet_period_seconds = $4,
		        effective_max_runtime_seconds = $5
		  WHERE id = $1`,
		runID, ackID, now, maxQuietSec, maxRuntimeSec,
	)
	if err != nil {
		return fmt.Errorf("postgres.RegisterAsyncAck: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres.RegisterAsyncAck: %s: %w", runID, persistence.ErrRunRowMissing)
	}
	return nil
}

func (q *queueImpl) BumpLastProgressAt(ctx context.Context, tx persistence.Tx, runID shared.UUID, now time.Time) (bool, error) {
	tag, err := q.q(tx).Exec(ctx,
		`UPDATE rimsky_node_runs SET last_progress_at = $2 WHERE id = $1`,
		runID, now,
	)
	if err != nil {
		return false, fmt.Errorf("postgres.BumpLastProgressAt: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func nilIfEmpty(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

func nilIfEmptyStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableText(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (q *queueImpl) ListLive(ctx context.Context, filter persistence.DispatchListFilter, pag persistence.ListPagination) (persistence.PaginatedListResult[persistence.DispatchRow], error) {
	limit := pag.Limit
	if limit <= 0 {
		limit = 50
	}
	var cursorEnq *time.Time
	var cursorID *shared.UUID
	if pag.Cursor != "" {
		oc, id, err := decodeDispatchCursor(pag.Cursor)
		if err != nil {
			return persistence.PaginatedListResult[persistence.DispatchRow]{}, fmt.Errorf("postgres.ListLive: bad cursor: %w", err)
		}
		cursorEnq = &oc
		cursorID = &id
	}
	var stateClaimed any
	switch filter.State {
	case "pending":
		stateClaimed = false
	case "claimed":
		stateClaimed = true
	}
	executor := nullableText(filter.ExecutorName)
	var instanceID any
	if filter.InstanceID != nil {
		instanceID = *filter.InstanceID
	}
	rows, err := q.pool.Query(ctx,
		`SELECT d.id, d.node_id, d.executor_name, d.required_stores, d.enqueued_at,
		        d.claimed_by, d.claimed_at, d.frame_id, d.async_ack_id,
		        d.async_ack_registered_at, d.last_progress_at, d.tags,
		        d.effective_max_quiet_period_seconds, d.effective_max_runtime_seconds
		   FROM rimsky_node_runs d
		   LEFT JOIN rimsky_nodes n ON n.id = d.node_id
		  WHERE d.phase IN ('pending','active','held','parked')
		    AND ($1::bool IS NULL OR (d.claimed_by IS NOT NULL) = $1)
		    AND ($2::text IS NULL OR d.executor_name = $2)
		    AND ($3::uuid IS NULL OR n.instance_id = $3)
		    AND ($4::timestamptz IS NULL OR (d.enqueued_at, d.id) < ($4, $5))
		  ORDER BY d.enqueued_at DESC, d.id DESC
		  LIMIT $6`,
		stateClaimed, executor, instanceID,
		nullableTime(cursorEnq), nullableUUID(cursorID),
		limit,
	)
	if err != nil {
		return persistence.PaginatedListResult[persistence.DispatchRow]{}, err
	}
	defer rows.Close()
	var out []persistence.DispatchRow
	for rows.Next() {
		var r persistence.DispatchRow
		if err := rows.Scan(
			&r.ID, &r.NodeID, &r.ExecutorName, &r.RequiredClaimProducers,
			&r.EnqueuedAt, &r.ClaimedBy, &r.ClaimedAt, &r.FrameID,
			&r.AsyncAckID, &r.AsyncAckRegisteredAt, &r.LastProgressAt, &r.Tags,
			&r.EffectiveMaxQuietPeriodSeconds, &r.EffectiveMaxRuntimeSeconds,
		); err != nil {
			return persistence.PaginatedListResult[persistence.DispatchRow]{}, err
		}
		if r.RequiredClaimProducers == nil {
			r.RequiredClaimProducers = []string{}
		}
		out = append(out, r)
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
	var stateClaimed any
	switch filter.State {
	case "pending":
		stateClaimed = false
	case "claimed":
		stateClaimed = true
	}
	executor := nullableText(filter.ExecutorName)
	var instanceID any
	if filter.InstanceID != nil {
		instanceID = *filter.InstanceID
	}
	var n int
	err := q.pool.QueryRow(ctx,
		`SELECT COUNT(*)
		   FROM rimsky_node_runs d
		   LEFT JOIN rimsky_nodes n ON n.id = d.node_id
		  WHERE d.phase IN ('pending','active','held','parked')
		    AND ($1::bool IS NULL OR (d.claimed_by IS NOT NULL) = $1)
		    AND ($2::text IS NULL OR d.executor_name = $2)
		    AND ($3::uuid IS NULL OR n.instance_id = $3)`,
		stateClaimed, executor, instanceID,
	).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

func (q *queueImpl) CountParkedByReason(ctx context.Context) (map[string]int, error) {
	rows, err := q.pool.Query(ctx,
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
	row := q.pool.QueryRow(ctx,
		`SELECT d.id, d.node_id, d.executor_name, d.required_stores, d.enqueued_at,
		        d.claimed_by, d.claimed_at, d.frame_id, d.async_ack_id,
		        d.async_ack_registered_at, d.last_progress_at, d.tags,
		        d.effective_max_quiet_period_seconds, d.effective_max_runtime_seconds
		   FROM rimsky_node_runs d
		  WHERE d.id = $1
		    AND d.phase IN ('pending','active','held','parked')`, id,
	)
	var r persistence.DispatchRow
	if err := row.Scan(
		&r.ID, &r.NodeID, &r.ExecutorName, &r.RequiredClaimProducers,
		&r.EnqueuedAt, &r.ClaimedBy, &r.ClaimedAt, &r.FrameID,
		&r.AsyncAckID, &r.AsyncAckRegisteredAt, &r.LastProgressAt, &r.Tags,
		&r.EffectiveMaxQuietPeriodSeconds, &r.EffectiveMaxRuntimeSeconds,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if r.RequiredClaimProducers == nil {
		r.RequiredClaimProducers = []string{}
	}
	return &r, nil
}

// @concept: run-scope
func (q *queueImpl) GetInFlightRunForNode(ctx context.Context, tx persistence.Tx, nodeID, runScopeID shared.UUID) (shared.UUID, bool, error) {
	ex := q.q(tx)
	var id shared.UUID
	err := ex.QueryRow(ctx,
		`SELECT id FROM rimsky_node_runs
		  WHERE node_id = $1 AND run_scope_id = $2
		    AND phase IN ('pending','active','held','parked')
		  LIMIT 1`,
		nodeID, runScopeID).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return shared.UUID{}, false, nil
		}
		return shared.UUID{}, false, fmt.Errorf("postgres.GetInFlightRunForNode: %w", err)
	}
	return id, true, nil
}

// @concept: run-scope
func (q *queueImpl) GetMostRecentRunForNodeInScope(ctx context.Context, tx persistence.Tx, nodeID, runScopeID shared.UUID) (shared.UUID, bool, error) {
	ex := q.q(tx)
	var id shared.UUID
	err := ex.QueryRow(ctx,
		`SELECT id FROM rimsky_node_runs
		  WHERE node_id = $1 AND run_scope_id = $2
		  ORDER BY enqueued_at DESC, id DESC
		  LIMIT 1`,
		nodeID, runScopeID).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return shared.UUID{}, false, nil
		}
		return shared.UUID{}, false, fmt.Errorf("postgres.GetMostRecentRunForNodeInScope: %w", err)
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
		return nil, errors.New("postgres.ListInFlightRunPhases: tx required")
	}
	rows, err := q.q(tx).Query(ctx,
		`SELECT DISTINCT node_id, phase FROM rimsky_node_runs
		  WHERE node_id = ANY($1)
		    AND frame_id = $2
		    AND run_scope_id = $3
		    AND phase IN ('pending','active','held','parked')`,
		nodeIDs, frameID, runScopeID)
	if err != nil {
		return nil, fmt.Errorf("postgres.ListInFlightRunPhases: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var nodeID shared.UUID
		var phase string
		if err := rows.Scan(&nodeID, &phase); err != nil {
			return nil, fmt.Errorf("postgres.ListInFlightRunPhases: scan: %w", err)
		}
		out[nodeID] = append(out[nodeID], phase)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres.ListInFlightRunPhases: rows: %w", err)
	}
	return out, nil
}

func encodeDispatchCursor(enqueued time.Time, id shared.UUID) string {
	c := struct {
		E time.Time   `json:"e"`
		I shared.UUID `json:"i"`
	}{E: enqueued, I: id}
	b, _ := json.Marshal(c)
	return base64.StdEncoding.EncodeToString(b)
}

func decodeDispatchCursor(s string) (time.Time, shared.UUID, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, shared.UUID{}, err
	}
	var c struct {
		E time.Time   `json:"e"`
		I shared.UUID `json:"i"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return time.Time{}, shared.UUID{}, err
	}
	return c.E, c.I, nil
}

func nullableUUID(p *shared.UUID) any {
	if p == nil {
		return nil
	}
	return *p
}
