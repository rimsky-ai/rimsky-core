// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	foundationshared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

const nodeCols = `
  n.id, n.instance_id, n.node_type, n.executor,
  n.tags, n.cascade_mode, n.created_at
`

const nodeSelect = `FROM rimsky_nodes n`

func (s *nodesImpl) Create(ctx context.Context, in persistence.NodeCreateInput, tx persistence.Tx) (persistence.NodeRow, error) {
	ex := s.q(tx)
	tags := in.Tags
	if tags == nil {
		tags = []string{}
	}
	cascadeMode := in.CascadeMode
	if cascadeMode == "" {
		cascadeMode = string(cascade.CascadeModeMostRecent)
	}
	if _, err := ex.Exec(ctx,
		`INSERT INTO rimsky_nodes (
		   id, instance_id, node_type, executor, tags, cascade_mode
		 ) VALUES ($1, $2, $3, $4, $5, $6)`,
		in.ID, in.InstanceID, in.NodeType,
		nullableString(in.Executor),
		tags, cascadeMode,
	); err != nil {
		return persistence.NodeRow{}, err
	}
	out, err := s.Get(ctx, in.ID, tx)
	if err != nil {
		return persistence.NodeRow{}, err
	}
	if out == nil {
		return persistence.NodeRow{}, fmt.Errorf("nodes.Create: row vanished after insert: %s", in.ID)
	}
	return *out, nil
}

func (s *nodesImpl) Get(ctx context.Context, id foundationshared.UUID, tx persistence.Tx) (*persistence.NodeRow, error) {
	ex := s.q(tx)
	row := ex.QueryRow(ctx,
		`SELECT `+nodeCols+` `+nodeSelect+` WHERE n.id = $1`, id)
	out, err := scanNode(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

func (s *nodesImpl) ListByInstance(ctx context.Context, instanceID foundationshared.UUID, tx persistence.Tx) ([]persistence.NodeRow, error) {
	ex := s.q(tx)
	rows, err := ex.Query(ctx,
		`SELECT `+nodeCols+` `+nodeSelect+`
		 WHERE n.instance_id = $1
		 ORDER BY n.created_at ASC`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectNodes(rows)
}

func (s *nodesImpl) ListByInstancePaged(
	ctx context.Context,
	instanceID foundationshared.UUID,
	pag persistence.ListPagination,
	tx persistence.Tx,
) (persistence.PaginatedListResult[persistence.NodeRow], error) {
	return s.ListByInstancePagedFiltered(ctx, instanceID, pag, persistence.NodeListFilter{}, tx)
}

func (s *nodesImpl) ListByInstancePagedFiltered(
	ctx context.Context,
	instanceID foundationshared.UUID,
	pag persistence.ListPagination,
	filter persistence.NodeListFilter,
	tx persistence.Tx,
) (persistence.PaginatedListResult[persistence.NodeRow], error) {
	ex := s.q(tx)
	limit := pag.Limit
	if limit <= 0 {
		limit = 100
	}
	var cursor *foundationshared.UUID
	if pag.Cursor != "" {
		u, err := uuid.Parse(pag.Cursor)
		if err != nil {
			return persistence.PaginatedListResult[persistence.NodeRow]{}, fmt.Errorf("nodes.listByInstancePaged: bad cursor: %w", err)
		}
		cursor = &u
	}
	rows, err := ex.Query(ctx,
		`SELECT `+nodeCols+` `+nodeSelect+`
		 WHERE n.instance_id = $1
		   AND (
		     $2::uuid IS NULL
		     OR (n.created_at, n.id) > (
		       (SELECT created_at FROM rimsky_nodes WHERE id = $2::uuid),
		       $2::uuid
		     )
		   )
		   AND ($4 = '' OR $4 = ANY(n.tags))
		 ORDER BY n.created_at ASC, n.id ASC
		 LIMIT $3`,
		instanceID, cursor, limit, filter.Tag,
	)
	if err != nil {
		return persistence.PaginatedListResult[persistence.NodeRow]{}, err
	}
	defer rows.Close()
	out, err := collectNodes(rows)
	if err != nil {
		return persistence.PaginatedListResult[persistence.NodeRow]{}, err
	}
	var nextCursor string
	if len(out) == limit && len(out) > 0 {
		nextCursor = out[len(out)-1].ID.String()
	}
	return persistence.PaginatedListResult[persistence.NodeRow]{Rows: out, NextCursor: nextCursor}, nil
}

// @concept: wait-set
func (s *nodesImpl) ListReadyForDispatch(ctx context.Context, tx persistence.Tx) ([]persistence.NodeRow, error) {
	ex := s.q(tx)
	rows, err := ex.Query(ctx,
		`SELECT DISTINCT `+nodeCols+`
		   FROM rimsky_nodes n
		   JOIN rimsky_node_runs nr ON nr.node_id = n.id
		  WHERE n.executor IS NOT NULL AND n.executor <> ''
		    AND nr.state = 'stale'
		    AND NOT EXISTS (
		      SELECT 1 FROM rimsky_wait_set w
		       WHERE w.frame_id = nr.frame_id AND w.receiver_run_id = nr.id
		         AND w.drained_at IS NULL
		    )
		  ORDER BY n.created_at ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectNodes(rows)
}

// @concept: wait-set
func (s *nodesImpl) ListPureCascadeReady(ctx context.Context, tx persistence.Tx) ([]persistence.PureCascadeReadyRow, error) {
	ex := s.q(tx)
	rows, err := ex.Query(ctx,
		`SELECT n.id, n.instance_id, n.node_type,
		        nr.id AS run_id, nr.run_scope_id, nr.frame_id
		   FROM rimsky_nodes n
		   JOIN rimsky_node_runs nr ON nr.node_id = n.id
		  WHERE (n.executor IS NULL OR n.executor = '')
		    AND nr.state = 'stale'
		    AND NOT EXISTS (
		      SELECT 1 FROM rimsky_wait_set w
		       WHERE w.frame_id = nr.frame_id AND w.receiver_run_id = nr.id
		         AND w.drained_at IS NULL
		    )
		  ORDER BY n.created_at ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []persistence.PureCascadeReadyRow
	for rows.Next() {
		var r persistence.PureCascadeReadyRow
		if err := rows.Scan(&r.NodeID, &r.InstanceID, &r.NodeType, &r.RunID, &r.RunScopeID, &r.FrameID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *nodesImpl) ListRunning(ctx context.Context, tx persistence.Tx) ([]persistence.NodeRow, error) {
	ex := s.q(tx)
	rows, err := ex.Query(ctx,
		`SELECT DISTINCT `+nodeCols+`
		   FROM rimsky_nodes n
		   JOIN rimsky_node_runs nr ON nr.node_id = n.id
		  WHERE nr.state = 'running'
		  ORDER BY n.created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectNodes(rows)
}

// @concept: supervisor
func (s *nodesImpl) CountRunningForSupervisor(ctx context.Context, supervisorID string, tx persistence.Tx) (int, error) {
	var n int
	err := s.q(tx).QueryRow(ctx,
		`SELECT count(*)::int FROM rimsky_node_runs
		  WHERE state = 'running' AND claimed_by = $1`,
		supervisorID,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("CountRunningForSupervisor: %w", err)
	}
	return n, nil
}

// @concept: node
func (s *nodesImpl) CountAllNodes(ctx context.Context, tx persistence.Tx) (int, error) {
	var n int
	err := s.q(tx).QueryRow(ctx, `SELECT count(*)::int FROM rimsky_nodes`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("CountAllNodes: %w", err)
	}
	return n, nil
}

// @concept: node
func (s *nodesImpl) CountDistinctNodesWithRuns(ctx context.Context, tx persistence.Tx) (int, error) {
	var n int
	err := s.q(tx).QueryRow(ctx,
		`SELECT count(DISTINCT node_id)::int FROM rimsky_node_runs`,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("CountDistinctNodesWithRuns: %w", err)
	}
	return n, nil
}

func (s *nodesImpl) CountByState(ctx context.Context, tx persistence.Tx) (map[cascade.NodeState]int, error) {
	ex := s.q(tx)
	rows, err := ex.Query(ctx,
		`SELECT state, count(*)::int
		   FROM rimsky_node_runs
		  GROUP BY state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[cascade.NodeState]int{
		cascade.NodeStatePending: 0,
		cascade.NodeStateStale:   0,
		cascade.NodeStateRunning: 0,
		cascade.NodeStateHeld:    0,
		cascade.NodeStateParked:  0,
		cascade.NodeStateFresh:   0,
		cascade.NodeStateFailed:  0,
	}
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			return nil, err
		}
		out[cascade.NodeState(state)] = count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *nodesImpl) UpdateState(
	ctx context.Context,
	nodeRunID foundationshared.UUID,
	state cascade.NodeState,
	reason cascade.TransitionReason,
	settlingSignalType *string,
	tx persistence.Tx,
) error {
	return s.enforceAndUpdate(ctx, s.q(tx), nodeRunID, state, reason, settlingSignalType)
}

func (s *nodesImpl) enforceAndUpdate(
	ctx context.Context,
	ex querier,
	nodeRunID foundationshared.UUID,
	state cascade.NodeState,
	reason cascade.TransitionReason,
	settlingSignalType *string,
) error {
	var (
		stateScan   string
		frameIDScan foundationshared.UUID
	)
	if err := ex.QueryRow(ctx,
		`SELECT state, frame_id
		   FROM rimsky_node_runs
		  WHERE id = $1
		  FOR UPDATE`, nodeRunID,
	).Scan(&stateScan, &frameIDScan); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("nodes.updateState: no node_run row for id %s", nodeRunID)
		}
		return fmt.Errorf("nodes.updateState: select run row: %w", err)
	}
	current := cascade.NodeState(stateScan)
	expected, err := cascade.NextState(current, reason)
	if err != nil {
		return err
	}
	if expected != state {
		return foundationshared.Wrap(cascade.ErrIllegalTransition,
			"illegal state transition target",
			map[string]any{
				"node_run_id": nodeRunID, "from": current, "requested": state,
				"computed": expected, "reason": reason.Kind,
			})
	}
	var settlingArg any
	if settlingSignalType == nil {
		settlingArg = nil
	} else {
		settlingArg = *settlingSignalType
	}
	if _, err := ex.Exec(ctx,
		`UPDATE rimsky_node_runs
		   SET state = $2,
		       settling_signal_type = COALESCE($3::text, settling_signal_type)
		 WHERE id = $1`,
		nodeRunID, string(state), settlingArg,
	); err != nil {
		return fmt.Errorf("nodes.updateState: run-row update: %w", err)
	}
	if _, err := ex.Exec(ctx,
		`UPDATE rimsky_frames SET last_progress_at = NOW() WHERE frame_id = $1`,
		frameIDScan,
	); err != nil {
		return fmt.Errorf("nodes.updateState: refresh frame progress: %w", err)
	}
	return nil
}

// @concept: error-policy
func (s *nodesImpl) UpdateRunEvaluatorState(ctx context.Context, runID foundationshared.UUID, es spec.EvaluatorState, tx persistence.Tx) error {
	_, err := s.q(tx).Exec(ctx,
		`UPDATE rimsky_node_runs
		   SET retry_counter = $2
		 WHERE id = $1`,
		runID, es.RetryCounter,
	)
	return err
}

// @concept: error-policy
func (s *nodesImpl) GetRunEvaluatorState(ctx context.Context, runID foundationshared.UUID, tx persistence.Tx) (spec.EvaluatorState, error) {
	var es spec.EvaluatorState
	err := s.q(tx).QueryRow(ctx,
		`SELECT retry_counter FROM rimsky_node_runs WHERE id = $1`,
		runID,
	).Scan(&es.RetryCounter)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return spec.EvaluatorState{}, nil
		}
		return spec.EvaluatorState{}, err
	}
	return es, nil
}

func (s *nodesImpl) ClearSettlingSignalType(ctx context.Context, id foundationshared.UUID, runScopeID foundationshared.UUID, tx persistence.Tx) error {
	ex := s.q(tx)
	_, err := ex.Exec(ctx,
		`UPDATE rimsky_node_runs SET settling_signal_type = NULL
		  WHERE node_id = $1
		    AND run_scope_id = $2
		    AND state IN ('pending','stale','running','held','parked')`, id, runScopeID)
	return err
}

func (s *nodesImpl) ResetFailedTerminalSettlingSignalType(ctx context.Context, id foundationshared.UUID, runScopeID foundationshared.UUID, tx persistence.Tx) error {
	ex := s.q(tx)
	_, err := ex.Exec(ctx,
		`WITH target AS (
		     SELECT id
		       FROM rimsky_node_runs
		      WHERE node_id = $1 AND run_scope_id = $2 AND state = 'failed'
		      ORDER BY COALESCE(active_terminal_at, enqueued_at) DESC
		      LIMIT 1
		 )
		 UPDATE rimsky_node_runs
		    SET settling_signal_type = NULL
		   FROM target
		  WHERE rimsky_node_runs.id = target.id`,
		id, runScopeID,
	)
	if err != nil {
		return fmt.Errorf("nodes.ResetFailedTerminalSettlingSignalType: %w", err)
	}
	return nil
}

func (s *nodesImpl) GetFailedTerminalRunScopeID(ctx context.Context, id foundationshared.UUID, tx persistence.Tx) (*foundationshared.UUID, error) {
	ex := s.q(tx)
	var scope foundationshared.UUID
	err := ex.QueryRow(ctx,
		`SELECT run_scope_id FROM rimsky_node_runs
		  WHERE node_id = $1 AND state = 'failed'
		  ORDER BY COALESCE(active_terminal_at, enqueued_at) DESC
		  LIMIT 1`, id,
	).Scan(&scope)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("nodes.GetFailedTerminalRunScopeID: %w", err)
	}
	return &scope, nil
}

func (s *nodesImpl) DeleteByInstance(ctx context.Context, instanceID foundationshared.UUID, tx persistence.Tx) error {
	ex := s.q(tx)
	_, err := ex.Exec(ctx, `DELETE FROM rimsky_nodes WHERE instance_id = $1`, instanceID)
	return err
}

// @concept: signal
// @concept: cascade
// @decision: mode-default-most-recent
func (s *nodesImpl) GetCascadeMode(ctx context.Context, nodeID foundationshared.UUID, tx persistence.Tx) (cascade.CascadeMode, error) {
	ex := s.q(tx)
	var mode string
	err := ex.QueryRow(ctx,
		`SELECT cascade_mode FROM rimsky_nodes WHERE id = $1`, nodeID,
	).Scan(&mode)
	if errors.Is(err, pgx.ErrNoRows) {
		return cascade.CascadeModeMostRecent, nil
	}
	if err != nil {
		return "", fmt.Errorf("GetCascadeMode: %w", err)
	}
	if mode == "" {
		return cascade.CascadeModeMostRecent, nil
	}
	return cascade.CascadeMode(mode), nil
}

// @concept: node
func (s *nodesImpl) GetRunSummary(ctx context.Context, nodeID foundationshared.UUID, tx persistence.Tx) (persistence.NodeRunSummary, error) {
	var out persistence.NodeRunSummary
	rows, err := s.q(tx).Query(ctx,
		`SELECT state, count(*)
		   FROM rimsky_node_runs
		  WHERE node_id = $1
		  GROUP BY state`,
		nodeID,
	)
	if err != nil {
		return out, fmt.Errorf("GetRunSummary: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			return out, fmt.Errorf("GetRunSummary: scan: %w", err)
		}
		switch cascade.NodeState(state) {
		case cascade.NodeStateRunning, cascade.NodeStateHeld, cascade.NodeStateParked:
			out.ActiveCount += count
		case cascade.NodeStatePending, cascade.NodeStateStale:
			out.PendingCount += count
		case cascade.NodeStateFresh:
			out.FreshCount += count
		case cascade.NodeStateFailed:
			out.FailedCount += count
		}
	}
	return out, rows.Err()
}

func (s *nodesImpl) HasRunForNodeInFrame(ctx context.Context, nodeID foundationshared.UUID, frameID foundationshared.UUID, tx persistence.Tx) (bool, error) {
	ex := s.q(tx)
	var exists bool
	err := ex.QueryRow(ctx,
		`SELECT EXISTS (
		   SELECT 1 FROM rimsky_node_runs
		    WHERE node_id = $1 AND frame_id = $2
		 )`, nodeID, frameID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("HasRunForNodeInFrame: %w", err)
	}
	return exists, nil
}

// @concept: cascade
// @concept: run-scope
func (s *nodesImpl) HasAdvancedSiblingInScope(
	ctx context.Context, tx persistence.Tx,
	nodeID, runScopeID, excludingRunID foundationshared.UUID,
) (bool, error) {
	var exists bool
	err := s.q(tx).QueryRow(ctx,
		`SELECT EXISTS (
		   SELECT 1 FROM rimsky_node_runs
		    WHERE node_id = $1 AND run_scope_id = $2 AND id <> $3
		      AND state IN ('stale', 'running', 'held', 'parked')
		 )`,
		nodeID, runScopeID, excludingRunID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("HasAdvancedSiblingInScope: %w", err)
	}
	return exists, nil
}

// @concept: cascade
// @concept: run-scope
func (s *nodesImpl) ListPendingSiblingRunsInScope(
	ctx context.Context, tx persistence.Tx, senderRunID foundationshared.UUID,
) ([]foundationshared.UUID, error) {
	rows, err := s.q(tx).Query(ctx,
		`SELECT pending.id
		   FROM rimsky_node_runs pending
		   JOIN rimsky_node_runs sender ON sender.id = $1
		  WHERE pending.node_id = sender.node_id
		    AND pending.run_scope_id = sender.run_scope_id
		    AND pending.id <> sender.id
		    AND pending.state = 'pending'
		  ORDER BY pending.sequence ASC`,
		senderRunID,
	)
	if err != nil {
		return nil, fmt.Errorf("ListPendingSiblingRunsInScope: %w", err)
	}
	defer rows.Close()
	var out []foundationshared.UUID
	for rows.Next() {
		var id foundationshared.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("ListPendingSiblingRunsInScope: scan: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// @concept: cascade
// @decision: mode-default-most-recent
func (s *nodesImpl) HasLaterCascadePending(
	ctx context.Context, tx persistence.Tx, nodeID, runScopeID foundationshared.UUID, afterSeq int64,
) (bool, error) {
	var exists bool
	err := s.q(tx).QueryRow(ctx,
		`SELECT EXISTS (
		   SELECT 1 FROM rimsky_node_runs
		    WHERE node_id = $1 AND run_scope_id = $2 AND sequence > $3
		      AND state IN ('pending','stale') AND creation_reason = 'cascade'
		      AND claimed_by IS NULL
		 )`,
		nodeID, runScopeID, afterSeq,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("HasLaterCascadePending: %w", err)
	}
	return exists, nil
}

// @concept: cascade
// @concept: run-scope
func (s *nodesImpl) ListPendingRunsInScopeForNodes(
	ctx context.Context, tx persistence.Tx, runScopeID foundationshared.UUID, nodeIDs []foundationshared.UUID,
) ([]foundationshared.UUID, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	rows, err := s.q(tx).Query(ctx,
		`SELECT id
		   FROM rimsky_node_runs
		  WHERE run_scope_id = $1
		    AND node_id = ANY($2)
		    AND state = 'pending'
		  ORDER BY sequence ASC`,
		runScopeID, nodeIDs,
	)
	if err != nil {
		return nil, fmt.Errorf("ListPendingRunsInScopeForNodes: %w", err)
	}
	defer rows.Close()
	var out []foundationshared.UUID
	for rows.Next() {
		var id foundationshared.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("ListPendingRunsInScopeForNodes: scan: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *nodesImpl) GetRunByDispatchIDForUpdate(ctx context.Context, dispatchID foundationshared.UUID, tx persistence.Tx) (*persistence.NodeRunForCallback, error) {
	ex := s.q(tx)
	var (
		r         persistence.NodeRunForCallback
		stateScan string
	)
	err := ex.QueryRow(ctx,
		`SELECT id, node_id, run_scope_id, frame_id, state
		   FROM rimsky_node_runs
		  WHERE id = $1
		  FOR UPDATE`, dispatchID,
	).Scan(&r.ID, &r.NodeID, &r.RunScopeID, &r.FrameID, &stateScan)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("GetRunByDispatchIDForUpdate: %w", err)
	}
	r.State = cascade.NodeState(stateScan)
	return &r, nil
}

func scanNode(sc scannable) (persistence.NodeRow, error) {
	var (
		r           persistence.NodeRow
		executor    *string
		tags        []string
		cascadeMode string
	)
	if err := sc.Scan(
		&r.ID, &r.InstanceID, &r.NodeType,
		&executor,
		&tags,
		&cascadeMode,
		&r.CreatedAt,
	); err != nil {
		return persistence.NodeRow{}, err
	}
	r.Executor = derefString(executor)
	if tags == nil {
		tags = []string{}
	}
	r.Tags = tags
	if cascadeMode == "" {
		cascadeMode = string(cascade.CascadeModeMostRecent)
	}
	r.CascadeMode = cascade.CascadeMode(cascadeMode)
	return r, nil
}

func collectNodes(rows pgx.Rows) ([]persistence.NodeRow, error) {
	var out []persistence.NodeRow
	for rows.Next() {
		r, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// @concept: cascade
// @decision: walker-rule-per-sender-node
func (s *nodesImpl) LockReceiverCascade(
	ctx context.Context, tx persistence.Tx, nodeID, runScopeID, frameID foundationshared.UUID,
) error {
	ex := s.q(tx)
	hash := cascadeLockHash(nodeID, runScopeID, frameID)
	_, err := ex.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, hash)
	if err != nil {
		return fmt.Errorf("LockReceiverCascade: %w", err)
	}
	return nil
}

// @concept: cascade
// @decision: walker-rule-per-sender-node
func cascadeLockHash(nodeID, runScopeID, frameID foundationshared.UUID) int64 {
	h := sha256.New()
	h.Write(nodeID[:])
	h.Write(runScopeID[:])
	h.Write(frameID[:])
	sum := h.Sum(nil)
	return int64(binary.BigEndian.Uint64(sum[:8]))
}

// @concept: cascade
// @decision: walker-rule-per-sender-node
func (s *nodesImpl) FindLatestCascadePending(
	ctx context.Context, tx persistence.Tx, nodeID, runScopeID, frameID foundationshared.UUID,
) (*persistence.NodeRunForGate, error) {
	ex := s.q(tx)
	row := ex.QueryRow(ctx,
		`SELECT id, node_id, run_scope_id, frame_id, sequence, state, creation_reason, COALESCE(claimed_by, '')
		   FROM rimsky_node_runs
		  WHERE node_id = $1 AND run_scope_id = $2 AND frame_id = $3
		    AND state = 'pending'
		    AND creation_reason = 'cascade'
		  ORDER BY sequence DESC
		  LIMIT 1`,
		nodeID, runScopeID, frameID,
	)
	return scanGateRow(row)
}

// @concept: cascade
// @decision: walker-rule-per-sender-node
// @decision: sequence-scope-monotonic
func (s *nodesImpl) CreateCascadePending(
	ctx context.Context, tx persistence.Tx, nodeID, runScopeID, frameID foundationshared.UUID,
) (foundationshared.UUID, error) {
	ex := s.q(tx)
	var newID foundationshared.UUID
	err := ex.QueryRow(ctx,
		`INSERT INTO rimsky_node_runs
		   (id, node_id, executor_name, required_stores, enqueued_at, state, creation_reason, sequence, frame_id, run_scope_id)
		 SELECT gen_random_uuid(), n.id,
		        NULLIF(n.executor, '') AS executor_name,
		        ARRAY[]::text[],
		        NOW(), 'pending', 'cascade',
		        COALESCE((SELECT MAX(sequence) FROM rimsky_node_runs WHERE node_id = $1 AND run_scope_id = $2), 0) + 1,
		        $3, $2
		   FROM rimsky_nodes n
		   JOIN rimsky_run_scopes rs ON rs.id = $2 AND rs.closed_at IS NULL
		  WHERE n.id = $1
		 RETURNING id`,
		nodeID, runScopeID, frameID,
	).Scan(&newID)
	if errors.Is(err, pgx.ErrNoRows) {
		var closedAt *time.Time
		serr := ex.QueryRow(ctx, `SELECT closed_at FROM rimsky_run_scopes WHERE id = $1`, runScopeID).Scan(&closedAt)
		if errors.Is(serr, pgx.ErrNoRows) {
			return foundationshared.UUID{}, fmt.Errorf("CreateCascadePending: run scope %s not found", runScopeID)
		}
		if serr != nil {
			return foundationshared.UUID{}, fmt.Errorf("CreateCascadePending: lookup run scope: %w", serr)
		}
		if closedAt != nil {
			return foundationshared.UUID{}, persistence.ErrRunScopeClosed
		}
		return foundationshared.UUID{}, fmt.Errorf("CreateCascadePending: node %s not found", nodeID)
	}
	if err != nil {
		return foundationshared.UUID{}, fmt.Errorf("CreateCascadePending: %w", err)
	}
	return newID, nil
}

// @concept: cascade
func (s *nodesImpl) GetRunForGate(ctx context.Context, tx persistence.Tx, runID foundationshared.UUID) (*persistence.NodeRunForGate, error) {
	row := s.q(tx).QueryRow(ctx,
		`SELECT id, node_id, run_scope_id, frame_id, sequence, state, creation_reason, COALESCE(claimed_by, '')
		   FROM rimsky_node_runs WHERE id = $1`, runID,
	)
	return scanGateRow(row)
}

// @concept: node-run
// @decision: sequence-scope-monotonic
func (s *nodesImpl) GetLatestRunForNode(
	ctx context.Context, tx persistence.Tx, nodeID foundationshared.UUID,
) (*persistence.NodeRunLatest, error) {
	row := s.q(tx).QueryRow(ctx,
		`SELECT id, node_id, run_scope_id, frame_id, sequence, state,
		        settling_signal_type, COALESCE(claimed_by, '')
		   FROM rimsky_node_runs
		  WHERE node_id = $1
		  ORDER BY CASE WHEN state IN ('pending','stale','running','held','parked') THEN 0 ELSE 1 END,
		           enqueued_at DESC, sequence DESC, id DESC
		  LIMIT 1`,
		nodeID,
	)
	return scanLatestRow(row)
}

// @concept: node-run
func (s *nodesImpl) ListRunsForInstanceByStates(
	ctx context.Context, tx persistence.Tx, instanceID foundationshared.UUID, states []cascade.NodeState,
) ([]persistence.NodeRunLatest, error) {
	if len(states) == 0 {
		return nil, nil
	}
	stateStrs := make([]string, 0, len(states))
	for _, st := range states {
		stateStrs = append(stateStrs, string(st))
	}
	rows, err := s.q(tx).Query(ctx,
		`SELECT r.id, r.node_id, r.run_scope_id, r.frame_id, r.sequence, r.state,
		        r.settling_signal_type, COALESCE(r.claimed_by, '')
		   FROM rimsky_node_runs r
		   JOIN rimsky_nodes n ON n.id = r.node_id
		  WHERE n.instance_id = $1 AND r.state = ANY($2)
		  ORDER BY r.sequence ASC`,
		instanceID, stateStrs,
	)
	if err != nil {
		return nil, fmt.Errorf("ListRunsForInstanceByStates: %w", err)
	}
	defer rows.Close()
	out := make([]persistence.NodeRunLatest, 0)
	for rows.Next() {
		var (
			r       persistence.NodeRunLatest
			state   string
			sigType *string
		)
		if err := rows.Scan(&r.RunID, &r.NodeID, &r.RunScopeID, &r.FrameID, &r.Sequence, &state, &sigType, &r.ClaimedBy); err != nil {
			return nil, fmt.Errorf("ListRunsForInstanceByStates: scan: %w", err)
		}
		r.State = cascade.NodeState(state)
		r.SettlingSignalType = sigType
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ListRunsForInstanceByStates: rows: %w", err)
	}
	return out, nil
}

func scanLatestRow(row pgx.Row) (*persistence.NodeRunLatest, error) {
	var (
		r       persistence.NodeRunLatest
		state   string
		sigType *string
	)
	err := row.Scan(&r.RunID, &r.NodeID, &r.RunScopeID, &r.FrameID, &r.Sequence, &state, &sigType, &r.ClaimedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scanLatestRow: %w", err)
	}
	r.State = cascade.NodeState(state)
	r.SettlingSignalType = sigType
	return &r, nil
}

// @concept: cascade
func (s *nodesImpl) GetPriorRunBySequence(
	ctx context.Context, tx persistence.Tx, nodeID, runScopeID foundationshared.UUID, beforeSeq int64,
) (*persistence.NodeRunForGate, error) {
	row := s.q(tx).QueryRow(ctx,
		`SELECT id, node_id, run_scope_id, frame_id, sequence, state, creation_reason, COALESCE(claimed_by, '')
		   FROM rimsky_node_runs
		  WHERE node_id = $1 AND run_scope_id = $2 AND sequence < $3
		  ORDER BY sequence DESC
		  LIMIT 1`,
		nodeID, runScopeID, beforeSeq,
	)
	return scanGateRow(row)
}

// @concept: cascade
// @decision: mode-default-most-recent
func (s *nodesImpl) DeletePriorCascadeStales(
	ctx context.Context, tx persistence.Tx, nodeID, runScopeID foundationshared.UUID, beforeSeq int64,
) (int, error) {
	tag, err := s.q(tx).Exec(ctx,
		`DELETE FROM rimsky_node_runs
		  WHERE node_id = $1 AND run_scope_id = $2 AND sequence < $3
		    AND state = 'stale' AND creation_reason = 'cascade' AND claimed_by IS NULL`,
		nodeID, runScopeID, beforeSeq,
	)
	if err != nil {
		return 0, fmt.Errorf("DeletePriorCascadeStales: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// @concept: cascade
func (s *nodesImpl) GetPriorCascadeStaleNotClaimed(
	ctx context.Context, tx persistence.Tx, nodeID, runScopeID foundationshared.UUID, beforeSeq int64,
) (*persistence.NodeRunForGate, error) {
	row := s.q(tx).QueryRow(ctx,
		`SELECT id, node_id, run_scope_id, frame_id, sequence, state, creation_reason, COALESCE(claimed_by, '')
		   FROM rimsky_node_runs
		  WHERE node_id = $1 AND run_scope_id = $2 AND sequence < $3
		    AND state IN ('pending','stale') AND creation_reason = 'cascade' AND claimed_by IS NULL
		  ORDER BY sequence DESC
		  LIMIT 1`,
		nodeID, runScopeID, beforeSeq,
	)
	return scanGateRow(row)
}

// @concept: cascade
func (s *nodesImpl) GetMostRecentSettledRun(
	ctx context.Context, tx persistence.Tx, nodeID, runScopeID foundationshared.UUID, beforeSeq int64,
) (*persistence.NodeRunForGate, error) {
	row := s.q(tx).QueryRow(ctx,
		`SELECT id, node_id, run_scope_id, frame_id, sequence, state, creation_reason, COALESCE(claimed_by, '')
		   FROM rimsky_node_runs
		  WHERE node_id = $1 AND run_scope_id = $2 AND sequence < $3
		    AND state = 'fresh'
		  ORDER BY sequence DESC
		  LIMIT 1`,
		nodeID, runScopeID, beforeSeq,
	)
	return scanGateRow(row)
}

// @concept: cascade
func (s *nodesImpl) DropPendingRun(
	ctx context.Context, tx persistence.Tx, runID foundationshared.UUID,
) error {
	tag, err := s.q(tx).Exec(ctx,
		`DELETE FROM rimsky_node_runs WHERE id = $1 AND state = 'pending'`, runID,
	)
	if err != nil {
		return fmt.Errorf("DropPendingRun: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("DropPendingRun: run %s not in pending state", runID)
	}
	return nil
}

// @concept: cascade
func (s *nodesImpl) TransitionPendingToStale(
	ctx context.Context, tx persistence.Tx, runID foundationshared.UUID, enqueuedAt time.Time,
) error {
	target, err := cascade.NextState(cascade.NodeStatePending, cascade.ReasonGateCleared)
	if err != nil {
		return fmt.Errorf("TransitionPendingToStale: state machine: %w", err)
	}
	if target != cascade.NodeStateStale {
		return fmt.Errorf("TransitionPendingToStale: unexpected target %s", target)
	}
	tag, err := s.q(tx).Exec(ctx,
		`UPDATE rimsky_node_runs
		    SET state = $3, enqueued_at = $2
		  WHERE id = $1 AND state = 'pending'`,
		runID, enqueuedAt, string(target),
	)
	if err != nil {
		return fmt.Errorf("TransitionPendingToStale: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("TransitionPendingToStale: run %s not in pending state", runID)
	}
	return nil
}

// @concept: cascade
// @decision: non-cascade-direct-to-stale
func (s *nodesImpl) CreateNonCascadeStale(
	ctx context.Context, tx persistence.Tx, in persistence.NonCascadeStaleInput,
) (foundationshared.UUID, error) {
	if in.FrameID == (foundationshared.UUID{}) {
		return foundationshared.UUID{}, fmt.Errorf("CreateNonCascadeStale: frame_id required")
	}
	if in.RunScopeID == (foundationshared.UUID{}) {
		return foundationshared.UUID{}, fmt.Errorf("CreateNonCascadeStale: run_scope_id required")
	}
	if in.CreationReason == "" {
		return foundationshared.UUID{}, fmt.Errorf("CreateNonCascadeStale: creation_reason required")
	}
	stores := in.RequiredClaimProducers
	if stores == nil {
		stores = []string{}
	}
	var priorID any
	if in.PriorDispatchID != nil {
		priorID = *in.PriorDispatchID
	}
	var newID foundationshared.UUID
	err := s.q(tx).QueryRow(ctx,
		`INSERT INTO rimsky_node_runs
		   (id, node_id, executor_name, required_stores, enqueued_at, state, creation_reason, sequence,
		    frame_id, run_scope_id, prior_dispatch_id, prior_dispatch_disposition,
		    scratch_inline, scratch_handle, scratch_handle_backend)
		 SELECT gen_random_uuid(), $1, $2, $3, $4, 'stale', $5,
		        COALESCE((SELECT MAX(sequence) FROM rimsky_node_runs WHERE node_id = $1 AND run_scope_id = $7), 0) + 1,
		        $6, rs.id, $8, $9, $10, $11, $12
		   FROM rimsky_run_scopes rs
		  WHERE rs.id = $7 AND rs.closed_at IS NULL
		 RETURNING id`,
		in.NodeID, nullableText(in.ExecutorName), stores, in.EnqueuedAt, string(in.CreationReason),
		in.FrameID, in.RunScopeID, priorID, nullableText(in.PriorDispatchDisposition),
		nilIfEmpty(in.InitialScratchInline), nilIfEmptyStr(in.InitialScratchHandle), nilIfEmptyStr(in.InitialScratchHandleBackend),
	).Scan(&newID)
	if errors.Is(err, pgx.ErrNoRows) {
		var closedAt *time.Time
		serr := s.q(tx).QueryRow(ctx, `SELECT closed_at FROM rimsky_run_scopes WHERE id = $1`, in.RunScopeID).Scan(&closedAt)
		if errors.Is(serr, pgx.ErrNoRows) {
			return foundationshared.UUID{}, fmt.Errorf("CreateNonCascadeStale: run scope %s not found", in.RunScopeID)
		}
		if serr != nil {
			return foundationshared.UUID{}, fmt.Errorf("CreateNonCascadeStale: lookup run scope: %w", serr)
		}
		if closedAt != nil {
			return foundationshared.UUID{}, persistence.ErrRunScopeClosed
		}
		return foundationshared.UUID{}, fmt.Errorf("CreateNonCascadeStale: insert returned no rows")
	}
	if err != nil {
		return foundationshared.UUID{}, fmt.Errorf("CreateNonCascadeStale: %w", err)
	}
	// @decision: non-cascade-direct-to-stale
	if err := (*nodeAttributesImpl)((*tablesImpl)(s)).SnapshotBagForNewRun(ctx, tx, newID, in.NodeID, in.RunScopeID); err != nil {
		return foundationshared.UUID{}, fmt.Errorf("CreateNonCascadeStale: snapshot bag: %w", err)
	}
	return newID, nil
}

func scanGateRow(row pgx.Row) (*persistence.NodeRunForGate, error) {
	var out persistence.NodeRunForGate
	var (
		stateScan  string
		reasonScan string
	)
	err := row.Scan(&out.RunID, &out.NodeID, &out.RunScopeID, &out.FrameID, &out.Sequence, &stateScan, &reasonScan, &out.ClaimedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scanGateRow: %w", err)
	}
	out.State = cascade.NodeState(stateScan)
	out.CreationReason = cascade.CreationReason(reasonScan)
	return &out, nil
}
