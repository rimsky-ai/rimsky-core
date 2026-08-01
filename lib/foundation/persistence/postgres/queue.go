// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

const defaultCandidateLimit = 100

type queueImpl struct {
	pool   *pgxpool.Pool
	tables *tablesImpl
}

func newQueue(pool *pgxpool.Pool) *queueImpl { return &queueImpl{pool: pool} }

func (q *queueImpl) setTables(t *tablesImpl) { q.tables = t }

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

// @concept: run-scope
// @decision: non-cascade-direct-to-stale
func (q *queueImpl) Enqueue(ctx context.Context, req persistence.DispatchRequest, tx persistence.Tx) error {
	if tx == nil {
		if q.tables == nil {
			return fmt.Errorf("postgres.Enqueue: queue not wired with tables")
		}
		return q.tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return q.Enqueue(ctx, req, tx)
		})
	}
	claimProducers := req.RequiredClaimProducers
	if claimProducers == nil {
		claimProducers = []string{}
	}
	executor := nullableString(req.ExecutorName)
	if req.FrameID == (shared.UUID{}) {
		return fmt.Errorf("postgres.Enqueue: frame_id required for node %s", req.NodeID)
	}
	if req.RunScopeID == (shared.UUID{}) {
		return fmt.Errorf("postgres.Enqueue: run_scope_id required for node %s", req.NodeID)
	}
	if q.tables == nil {
		return fmt.Errorf("postgres.Enqueue: queue not wired with tables (cannot snapshot dispatch_input_bag)")
	}
	var priorRunPtr any
	if req.PriorNodeRunID != nil {
		priorRunPtr = *req.PriorNodeRunID
	}
	priorDisposition := nullableString(req.PriorDispatchDisposition)
	creationReason := req.CreationReason
	if creationReason == "" {
		creationReason = cascade.CreationReasonCascade
	}
	// @concept: executor
	var newRunID shared.UUID
	err := q.q(tx).QueryRow(ctx,
		`INSERT INTO rimsky_node_runs (id, node_id, executor_name, required_claim_producers, enqueued_at, state, creation_reason, sequence, frame_id, run_scope_id, prior_dispatch_id, prior_dispatch_disposition, scratch_inline, scratch_handle, scratch_handle_backend)
		 SELECT gen_random_uuid(), $1, $2, $3, $4, 'stale', $7,
		        COALESCE((SELECT MAX(sequence) FROM rimsky_node_runs WHERE node_id = $1 AND run_scope_id = $6), 0) + 1,
		        $5, rs.id, $8, $9, $10, $11, $12
		   FROM rimsky_run_scopes rs
		  WHERE rs.id = $6
		    AND rs.closed_at IS NULL
		 RETURNING id`,
		req.NodeID, executor, claimProducers, req.EnqueuedAt, req.FrameID, req.RunScopeID,
		string(creationReason),
		priorRunPtr, priorDisposition,
		nilIfEmpty(req.InitialScratchInline), nullableString(req.InitialScratchHandle), nullableString(req.InitialScratchHandleBackend),
	).Scan(&newRunID)
	if errors.Is(err, pgx.ErrNoRows) {
		var closedAt *time.Time
		serr := q.q(tx).QueryRow(ctx,
			`SELECT closed_at FROM rimsky_run_scopes WHERE id = $1`,
			req.RunScopeID,
		).Scan(&closedAt)
		if errors.Is(serr, pgx.ErrNoRows) {
			return fmt.Errorf("postgres.Enqueue: run scope %s not found", req.RunScopeID)
		}
		if serr != nil {
			return fmt.Errorf("postgres.Enqueue: lookup run scope: %w", serr)
		}
		if closedAt != nil {
			return persistence.ErrRunScopeClosed
		}
		return fmt.Errorf("postgres.Enqueue: insert returned no rows")
	}
	if err != nil {
		return fmt.Errorf("postgres.Enqueue: %w", err)
	}
	if err := (*nodeAttributesImpl)(q.tables).SnapshotBagForNewRun(ctx, newRunID, req.NodeID, req.RunScopeID, tx); err != nil {
		return fmt.Errorf("postgres.Enqueue: snapshot bag: %w", err)
	}
	return nil
}

func (q *queueImpl) SelectCandidates(
	ctx context.Context, req persistence.SelectCandidatesRequest, tx persistence.Tx,
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

	rows, err := pgT.Query(ctx,
		`SELECT d.id, d.node_id, n.node_type, d.executor_name, d.required_claim_producers, d.enqueued_at, d.frame_id,
		        d.prior_dispatch_id, d.prior_dispatch_disposition
		   FROM rimsky_node_runs d
		   JOIN rimsky_nodes n ON n.id = d.node_id
		   JOIN rimsky_instances i ON i.id = n.instance_id
		  WHERE d.claimed_by IS NULL
		    AND d.state = 'stale'
		    AND i.paused = false
		    AND i.terminated_at IS NULL
		    AND (
		      d.executor_name IS NOT NULL
		      OR (d.required_claim_producers IS NOT NULL AND cardinality(d.required_claim_producers) > 0)
		    )
		    AND d.enqueued_at <= NOW()
		    AND (d.enqueued_at > $2 OR (d.enqueued_at = $2 AND d.id > $3))
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
		  ORDER BY d.enqueued_at, d.id
		  LIMIT $1
		  FOR UPDATE OF d SKIP LOCKED`,
		limit, req.CursorEnqueuedAfter, req.CursorAfterNodeRunID,
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
			priorRunPtr      *shared.UUID
			priorDisposition *string
		)
		if err := rows.Scan(
			&c.NodeRunID, &c.NodeID, &c.NodeType,
			&executorName, &c.RequiredClaimProducers, &c.EnqueuedAt, &c.FrameID,
			&priorRunPtr, &priorDisposition,
		); err != nil {
			return nil, fmt.Errorf("postgres.SelectCandidates: scan: %w", err)
		}
		if executorName != nil {
			c.ExecutorName = *executorName
		}
		if c.RequiredClaimProducers == nil {
			c.RequiredClaimProducers = []string{}
		}
		c.PriorNodeRunID = priorRunPtr
		if priorDisposition != nil {
			c.PriorDispatchDisposition = *priorDisposition
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres.SelectCandidates: rows: %w", err)
	}
	return out, nil
}

func (q *queueImpl) ClaimDispatchRow(
	ctx context.Context, nodeRunID shared.UUID, supervisorID string, tx persistence.Tx,
) (bool, error) {
	if tx == nil {
		return false, errors.New("postgres.ClaimDispatchRow: tx required")
	}
	cmd, err := q.q(tx).Exec(ctx,
		`UPDATE rimsky_node_runs
		    SET claimed_by = $1, claimed_at = NOW(), last_progress_at = NOW()
		  WHERE id = $2 AND (claimed_by IS NULL OR claimed_by = $1) AND state = 'stale'
		    AND NOT EXISTS (
		      SELECT 1 FROM rimsky_node_runs other
		       WHERE other.node_id = rimsky_node_runs.node_id
		         AND other.run_scope_id = rimsky_node_runs.run_scope_id
		         AND other.id <> rimsky_node_runs.id
		         AND (other.claimed_by IS NOT NULL OR other.state IN ('held','parked'))
		    )`,
		supervisorID, nodeRunID,
	)
	if err != nil {
		return false, fmt.Errorf("postgres.ClaimDispatchRow: %w", err)
	}
	return cmd.RowsAffected() == 1, nil
}

func (q *queueImpl) PromoteClaimedToRunning(
	ctx context.Context, nodeRunID shared.UUID, supervisorID string, tx persistence.Tx,
) (bool, error) {
	if tx == nil {
		return false, errors.New("postgres.PromoteClaimedToRunning: tx required")
	}
	cmd, err := q.q(tx).Exec(ctx,
		`UPDATE rimsky_node_runs
		    SET state = 'running', last_progress_at = NOW()
		  WHERE id = $1 AND claimed_by = $2 AND state = 'stale'`,
		nodeRunID, supervisorID,
	)
	if err != nil {
		return false, fmt.Errorf("postgres.PromoteClaimedToRunning: %w", err)
	}
	return cmd.RowsAffected() == 1, nil
}

func (q *queueImpl) Complete(ctx context.Context, nodeRunID shared.UUID, expectedClaimedBy string) error {
	_, err := q.pool.Exec(ctx,
		`UPDATE rimsky_node_runs
		    SET claimed_by = NULL,
		        active_terminal_at = NOW()
		  WHERE id = $1
		    AND claimed_by = $2`,
		nodeRunID, expectedClaimedBy,
	)
	if err != nil {
		return fmt.Errorf("postgres.Complete: %w", err)
	}
	return nil
}

func (q *queueImpl) ForceComplete(ctx context.Context, nodeRunID shared.UUID) error {
	_, err := q.pool.Exec(ctx,
		`UPDATE rimsky_node_runs
		    SET claimed_by = NULL,
		        active_terminal_at = NOW()
		  WHERE id = $1`,
		nodeRunID,
	)
	if err != nil {
		return fmt.Errorf("postgres.ForceComplete: %w", err)
	}
	return nil
}

// @concept: run-scope
func (q *queueImpl) RemoveForNode(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID, expectedClaimedBy string, tx persistence.Tx) error {
	_, err := q.q(tx).Exec(ctx,
		`UPDATE rimsky_node_runs
		    SET claimed_by = NULL,
		        active_terminal_at = NOW()
		  WHERE node_id = $1
		    AND run_scope_id = $2
		    AND claimed_by = $3`,
		nodeID, runScopeID, expectedClaimedBy,
	)
	if err != nil {
		return fmt.Errorf("postgres.RemoveForNode: %w", err)
	}
	return nil
}

func (q *queueImpl) ForceRemoveForNode(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID, tx persistence.Tx) error {
	_, err := q.q(tx).Exec(ctx,
		`UPDATE rimsky_node_runs
		    SET claimed_by = NULL,
		        active_terminal_at = NOW()
		  WHERE node_id = $1
		    AND run_scope_id = $2
		    AND claimed_by IS NOT NULL`,
		nodeID, runScopeID,
	)
	if err != nil {
		return fmt.Errorf("postgres.ForceRemoveForNode: %w", err)
	}
	return nil
}

func (q *queueImpl) ListOrphanedClaims(ctx context.Context) ([]persistence.DispatchRow, error) {
	rows, err := q.pool.Query(ctx,
		`SELECT id, node_id, executor_name, required_claim_producers, enqueued_at,
		        claimed_by, claimed_at, frame_id, async_ack_id, async_ack_registered_at,
		        last_progress_at, tags,
		        effective_max_quiet_period_seconds, effective_max_runtime_seconds,
		        async_ack_principal, async_callback_url
		   FROM rimsky_node_runs
		  WHERE claimed_by IS NOT NULL
		    AND async_ack_id IS NOT NULL`,
	)
	if err != nil {
		return nil, fmt.Errorf("postgres.ListOrphanedClaims: %w", err)
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
			&r.AsyncAckPrincipal, &r.AsyncCallbackURL,
		); err != nil {
			return nil, fmt.Errorf("postgres.ListOrphanedClaims: scan: %w", err)
		}
		if r.RequiredClaimProducers == nil {
			r.RequiredClaimProducers = []string{}
		}
		r.Tags = dedupTags(r.Tags)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres.ListOrphanedClaims: rows: %w", err)
	}
	return out, nil
}

func (q *queueImpl) ReleaseClaim(ctx context.Context, nodeRunID shared.UUID, expectedClaimedBy string) error {
	_, err := q.pool.Exec(ctx,
		`UPDATE rimsky_node_runs
		    SET claimed_by = NULL, claimed_at = NULL, state = 'stale'
		  WHERE id = $1 AND claimed_by = $2 AND state NOT IN ('fresh', 'failed')`,
		nodeRunID, expectedClaimedBy,
	)
	if err != nil {
		return fmt.Errorf("postgres.ReleaseClaim: %w", err)
	}
	return nil
}

func (q *queueImpl) ForceReleaseClaim(ctx context.Context, nodeRunID shared.UUID) error {
	_, err := q.pool.Exec(ctx,
		`UPDATE rimsky_node_runs
		    SET claimed_by = NULL, claimed_at = NULL, state = 'stale'
		  WHERE id = $1`,
		nodeRunID,
	)
	if err != nil {
		return fmt.Errorf("postgres.ForceReleaseClaim: %w", err)
	}
	return nil
}

// @concept: node-run
func (q *queueImpl) ReleaseClaimWithDisposition(ctx context.Context, nodeRunID shared.UUID, expectedClaimedBy string, disposition string) error {
	if disposition == "" {
		return errors.New("postgres.ReleaseClaimWithDisposition: disposition required")
	}
	_, err := q.pool.Exec(ctx,
		`UPDATE rimsky_node_runs
		    SET claimed_by = NULL, claimed_at = NULL, state = 'stale',
		        prior_dispatch_id = id, prior_dispatch_disposition = $1
		  WHERE id = $2 AND claimed_by = $3 AND state NOT IN ('fresh', 'failed')`,
		disposition, nodeRunID, expectedClaimedBy,
	)
	if err != nil {
		return fmt.Errorf("postgres.ReleaseClaimWithDisposition: %w", err)
	}
	return nil
}

// @concept: node-run
func (q *queueImpl) StampPriorDispatch(ctx context.Context, nodeRunID shared.UUID, priorNodeRunID shared.UUID, disposition string, tx persistence.Tx) error {
	if tx == nil {
		return errors.New("postgres.StampPriorDispatch: tx required")
	}
	if disposition == "" {
		return errors.New("postgres.StampPriorDispatch: disposition required")
	}
	cmd, err := q.q(tx).Exec(ctx,
		`UPDATE rimsky_node_runs
		    SET prior_dispatch_id = $1, prior_dispatch_disposition = $2
		  WHERE id = $3`,
		priorNodeRunID, disposition, nodeRunID,
	)
	if err != nil {
		return fmt.Errorf("postgres.StampPriorDispatch: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return fmt.Errorf("postgres.StampPriorDispatch: %s: %w", nodeRunID, persistence.ErrNotFound)
	}
	return nil
}

func (q *queueImpl) GetDispatchNode(ctx context.Context, nodeRunID shared.UUID, tx persistence.Tx) (shared.UUID, persistence.ClaimOwnership, error) {
	return q.getDispatchNode(ctx, q.q(tx), nodeRunID)
}

func (q *queueImpl) getDispatchNode(ctx context.Context, exec querier, nodeRunID shared.UUID) (shared.UUID, persistence.ClaimOwnership, error) {
	var (
		nodeID    shared.UUID
		claimedBy *string
	)
	err := exec.QueryRow(ctx,
		`SELECT node_id, claimed_by FROM rimsky_node_runs WHERE id = $1`,
		nodeRunID,
	).Scan(&nodeID, &claimedBy)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return shared.UUID{}, persistence.ClaimOwnership{Kind: persistence.ClaimOwnershipKindNotFound}, nil
		}
		return shared.UUID{}, persistence.ClaimOwnership{}, err
	}
	if claimedBy == nil {
		return nodeID, persistence.ClaimOwnership{Kind: persistence.ClaimOwnershipKindUnclaimed}, nil
	}
	return nodeID, persistence.ClaimOwnership{Kind: persistence.ClaimOwnershipKindClaimedBy, SupervisorID: *claimedBy}, nil
}

func (q *queueImpl) GetClaimedBy(ctx context.Context, nodeRunID shared.UUID) (persistence.ClaimOwnership, error) {
	var claimedBy *string
	err := q.pool.QueryRow(ctx,
		`SELECT claimed_by FROM rimsky_node_runs WHERE id = $1`,
		nodeRunID,
	).Scan(&claimedBy)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return persistence.ClaimOwnership{Kind: persistence.ClaimOwnershipKindNotFound}, nil
		}
		return persistence.ClaimOwnership{}, err
	}
	if claimedBy == nil {
		return persistence.ClaimOwnership{Kind: persistence.ClaimOwnershipKindUnclaimed}, nil
	}
	return persistence.ClaimOwnership{Kind: persistence.ClaimOwnershipKindClaimedBy, SupervisorID: *claimedBy}, nil
}

func (q *queueImpl) LookupRunByAsyncAckID(ctx context.Context, ackID string, tx persistence.Tx) (*persistence.DispatchRow, error) {
	if tx == nil {
		return nil, errors.New("postgres.LookupRunByAsyncAckID: tx required")
	}
	row := q.q(tx).QueryRow(ctx,
		`SELECT id, node_id, executor_name, required_claim_producers, enqueued_at,
		        claimed_by, claimed_at, frame_id, async_ack_id, async_ack_registered_at,
		        last_progress_at, tags,
		        effective_max_quiet_period_seconds, effective_max_runtime_seconds,
		        async_ack_principal, async_callback_url
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
		&r.AsyncAckPrincipal, &r.AsyncCallbackURL,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("postgres.LookupRunByAsyncAckID: %w", err)
	}
	if r.RequiredClaimProducers == nil {
		r.RequiredClaimProducers = []string{}
	}
	r.Tags = dedupTags(r.Tags)
	return &r, nil
}

func (q *queueImpl) RegisterAsyncAck(ctx context.Context, runID shared.UUID, ackID string, now time.Time, maxQuietSec *int, maxRuntimeSec *int, expectedPrincipal string, callbackURL string, tx persistence.Tx) error {
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
		        effective_max_runtime_seconds = $5,
		        async_ack_principal = $6,
		        async_callback_url = $7
		  WHERE id = $1`,
		runID, ackID, now, maxQuietSec, maxRuntimeSec, nullableString(expectedPrincipal),
		nullableString(callbackURL),
	)
	if err != nil {
		return fmt.Errorf("postgres.RegisterAsyncAck: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres.RegisterAsyncAck: %s: %w", runID, persistence.ErrNotFound)
	}
	return nil
}

func (q *queueImpl) BumpLastProgressAt(ctx context.Context, runID shared.UUID, now time.Time, tx persistence.Tx) (bool, error) {
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
	executor := nullableString(filter.ExecutorName)
	var instanceID any
	if filter.InstanceID != nil {
		instanceID = *filter.InstanceID
	}
	rows, err := q.pool.Query(ctx,
		`SELECT d.id, d.node_id, d.state, d.executor_name, d.required_claim_producers, d.enqueued_at,
		        d.claimed_by, d.claimed_at, d.frame_id, d.async_ack_id,
		        d.async_ack_registered_at, d.last_progress_at, d.tags,
		        d.effective_max_quiet_period_seconds, d.effective_max_runtime_seconds,
		        d.async_ack_principal, d.async_callback_url
		   FROM rimsky_node_runs d
		   LEFT JOIN rimsky_nodes n ON n.id = d.node_id
		  WHERE d.state IN (`+inFlightNodeRunStates+`)
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
		var state string
		if err := rows.Scan(
			&r.ID, &r.NodeID, &state, &r.ExecutorName, &r.RequiredClaimProducers,
			&r.EnqueuedAt, &r.ClaimedBy, &r.ClaimedAt, &r.FrameID,
			&r.AsyncAckID, &r.AsyncAckRegisteredAt, &r.LastProgressAt, &r.Tags,
			&r.EffectiveMaxQuietPeriodSeconds, &r.EffectiveMaxRuntimeSeconds,
			&r.AsyncAckPrincipal, &r.AsyncCallbackURL,
		); err != nil {
			return persistence.PaginatedListResult[persistence.DispatchRow]{}, err
		}
		r.State = cascade.NodeState(state)
		if r.RequiredClaimProducers == nil {
			r.RequiredClaimProducers = []string{}
		}
		r.Tags = dedupTags(r.Tags)
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
	executor := nullableString(filter.ExecutorName)
	var instanceID any
	if filter.InstanceID != nil {
		instanceID = *filter.InstanceID
	}
	var n int
	err := q.pool.QueryRow(ctx,
		`SELECT COUNT(*)
		   FROM rimsky_node_runs d
		   LEFT JOIN rimsky_nodes n ON n.id = d.node_id
		  WHERE d.state IN (`+inFlightNodeRunStates+`)
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

func (q *queueImpl) CountParked(ctx context.Context) (int, error) {
	var n int
	err := q.pool.QueryRow(ctx,
		`SELECT COUNT(*)
		   FROM rimsky_node_runs
		  WHERE state = 'parked'`).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

func (q *queueImpl) GetByID(ctx context.Context, id shared.UUID) (*persistence.DispatchRow, error) {
	row := q.pool.QueryRow(ctx,
		`SELECT d.id, d.node_id, d.state, d.executor_name, d.required_claim_producers, d.enqueued_at,
		        d.claimed_by, d.claimed_at, d.frame_id, d.async_ack_id,
		        d.async_ack_registered_at, d.last_progress_at, d.tags,
		        d.effective_max_quiet_period_seconds, d.effective_max_runtime_seconds,
		        d.async_ack_principal, d.async_callback_url
		   FROM rimsky_node_runs d
		  WHERE d.id = $1
		    AND d.state IN (`+inFlightNodeRunStates+`)`, id,
	)
	var r persistence.DispatchRow
	var state string
	if err := row.Scan(
		&r.ID, &r.NodeID, &state, &r.ExecutorName, &r.RequiredClaimProducers,
		&r.EnqueuedAt, &r.ClaimedBy, &r.ClaimedAt, &r.FrameID,
		&r.AsyncAckID, &r.AsyncAckRegisteredAt, &r.LastProgressAt, &r.Tags,
		&r.EffectiveMaxQuietPeriodSeconds, &r.EffectiveMaxRuntimeSeconds,
		&r.AsyncAckPrincipal, &r.AsyncCallbackURL,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	r.State = cascade.NodeState(state)
	if r.RequiredClaimProducers == nil {
		r.RequiredClaimProducers = []string{}
	}
	r.Tags = dedupTags(r.Tags)
	return &r, nil
}

// @concept: run-scope
func (q *queueImpl) GetInFlightRunForNode(ctx context.Context, nodeID, runScopeID shared.UUID, tx persistence.Tx) (shared.UUID, bool, error) {
	ex := q.q(tx)
	var id shared.UUID
	err := ex.QueryRow(ctx,
		`SELECT id FROM rimsky_node_runs
		  WHERE node_id = $1 AND run_scope_id = $2
		    AND state IN (`+inFlightNodeRunStates+`)
		  ORDER BY sequence ASC
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
func (q *queueImpl) GetMostRecentRunForNodeInScope(ctx context.Context, nodeID, runScopeID shared.UUID, tx persistence.Tx) (shared.UUID, bool, error) {
	ex := q.q(tx)
	var id shared.UUID
	err := ex.QueryRow(ctx,
		`SELECT id FROM rimsky_node_runs
		  WHERE node_id = $1 AND run_scope_id = $2
		  ORDER BY sequence DESC
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
func (q *queueImpl) ListInFlightRunStates(
	ctx context.Context, nodeIDs []shared.UUID, frameID, runScopeID shared.UUID, tx persistence.Tx,
) (map[shared.UUID][]string, error) {
	out := map[shared.UUID][]string{}
	if len(nodeIDs) == 0 {
		return out, nil
	}
	if tx == nil {
		return nil, errors.New("postgres.ListInFlightRunStates: tx required")
	}
	rows, err := q.q(tx).Query(ctx,
		`SELECT DISTINCT node_id, state FROM rimsky_node_runs
		  WHERE node_id = ANY($1)
		    AND frame_id = $2
		    AND run_scope_id = $3
		    AND state IN (`+inFlightNodeRunStates+`)`,
		nodeIDs, frameID, runScopeID)
	if err != nil {
		return nil, fmt.Errorf("postgres.ListInFlightRunStates: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var nodeID shared.UUID
		var state string
		if err := rows.Scan(&nodeID, &state); err != nil {
			return nil, fmt.Errorf("postgres.ListInFlightRunStates: scan: %w", err)
		}
		out[nodeID] = append(out[nodeID], state)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres.ListInFlightRunStates: rows: %w", err)
	}
	return out, nil
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
