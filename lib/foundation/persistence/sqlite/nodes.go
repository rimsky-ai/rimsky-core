// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	foundationshared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

const nodeCols = `
  n.id, n.instance_id, n.node_type, n.executor,
  n.frame_id, n.tags, n.cascade_mode, n.created_at, n.updated_at
`

const nodeSelect = `FROM rimsky_nodes n`

func (s *nodesImpl) Create(ctx context.Context, in persistence.NodeCreateInput, tx persistence.Tx) (persistence.NodeRow, error) {
	now := nowUTC()
	tagsJSON, terr := encodeTagsJSON(in.Tags)
	if terr != nil {
		return persistence.NodeRow{}, fmt.Errorf("nodes.Create: encode tags: %w", terr)
	}
	cascadeMode := in.CascadeMode
	if cascadeMode == "" {
		cascadeMode = string(cascade.CascadeModeMostRecent)
	}
	if _, err := s.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_nodes (
		   id, instance_id, node_type, executor, tags, cascade_mode, created_at, updated_at
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		in.ID.String(), in.InstanceID.String(), in.NodeType,
		nullableString(in.Executor),
		tagsJSON, cascadeMode,
		now, now,
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
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT `+nodeCols+` `+nodeSelect+` WHERE n.id = ?`, id.String())
	out, err := scanNode(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

func (s *nodesImpl) ListByInstance(ctx context.Context, instanceID foundationshared.UUID, tx persistence.Tx) ([]persistence.NodeRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+nodeCols+` `+nodeSelect+`
		 WHERE n.instance_id = ?
		 ORDER BY n.created_at ASC`, instanceID.String())
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
	limit := pag.Limit
	if limit <= 0 {
		limit = 100
	}
	var cursor any
	if pag.Cursor != "" {
		u, err := uuid.Parse(pag.Cursor)
		if err != nil {
			return persistence.PaginatedListResult[persistence.NodeRow]{}, fmt.Errorf("nodes.listByInstancePaged: bad cursor: %w", err)
		}
		cursor = u.String()
	}
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+nodeCols+` `+nodeSelect+`
		 WHERE n.instance_id = ?
		   AND (
		     ? IS NULL
		     OR (n.created_at, n.id) > (
		       (SELECT created_at FROM rimsky_nodes WHERE id = ?),
		       ?
		     )
		   )
		   AND (
		     ? = ''
		     OR EXISTS (SELECT 1 FROM json_each(n.tags) WHERE value = ?)
		   )
		 ORDER BY n.created_at ASC, n.id ASC
		 LIMIT ?`,
		instanceID.String(), cursor, cursor, cursor,
		filter.Tag, filter.Tag,
		limit,
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
	rows, err := s.q(tx).QueryContext(ctx,
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
	rows, err := s.q(tx).QueryContext(ctx,
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
		var (
			r           persistence.PureCascadeReadyRow
			nodeIDStr   string
			instIDStr   string
			runIDStr    string
			runScopeStr string
			frameIDStr  string
		)
		if err := rows.Scan(&nodeIDStr, &instIDStr, &r.NodeType, &runIDStr, &runScopeStr, &frameIDStr); err != nil {
			return nil, err
		}
		if r.NodeID, err = uuid.Parse(nodeIDStr); err != nil {
			return nil, err
		}
		if r.InstanceID, err = uuid.Parse(instIDStr); err != nil {
			return nil, err
		}
		if r.RunID, err = uuid.Parse(runIDStr); err != nil {
			return nil, err
		}
		if r.RunScopeID, err = uuid.Parse(runScopeStr); err != nil {
			return nil, err
		}
		if r.FrameID, err = uuid.Parse(frameIDStr); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *nodesImpl) ListRunning(ctx context.Context, tx persistence.Tx) ([]persistence.NodeRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT DISTINCT `+nodeCols+`
		   FROM rimsky_nodes n
		   JOIN rimsky_node_runs nr ON nr.node_id = n.id
		  WHERE nr.state = 'running'
		  ORDER BY n.updated_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectNodes(rows)
}

// @concept: supervisor
func (s *nodesImpl) CountRunningForSupervisor(ctx context.Context, supervisorID string, tx persistence.Tx) (int, error) {
	var n int
	err := s.q(tx).QueryRowContext(ctx,
		`SELECT count(*) FROM rimsky_node_runs
		  WHERE state = 'running' AND claimed_by = ?`,
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
	err := s.q(tx).QueryRowContext(ctx, `SELECT count(*) FROM rimsky_nodes`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("CountAllNodes: %w", err)
	}
	return n, nil
}

// @concept: node
func (s *nodesImpl) CountDistinctNodesWithRuns(ctx context.Context, tx persistence.Tx) (int, error) {
	var n int
	err := s.q(tx).QueryRowContext(ctx,
		`SELECT count(DISTINCT node_id) FROM rimsky_node_runs`,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("CountDistinctNodesWithRuns: %w", err)
	}
	return n, nil
}

func (s *nodesImpl) CountByState(ctx context.Context, tx persistence.Tx) (map[cascade.NodeState]int, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT state, count(*)
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
	id foundationshared.UUID,
	runScopeID foundationshared.UUID,
	state cascade.NodeState,
	reason cascade.TransitionReason,
	settlingSignalType *string,
	tx persistence.Tx,
) error {
	return s.enforceAndUpdate(ctx, s.q(tx), id, runScopeID, state, reason, settlingSignalType)
}

func (s *nodesImpl) enforceAndUpdate(
	ctx context.Context,
	ex querier,
	id foundationshared.UUID,
	runScopeID foundationshared.UUID,
	state cascade.NodeState,
	reason cascade.TransitionReason,
	settlingSignalType *string,
) error {
	current := cascade.NodeStateFresh
	var (
		runIDStr      sql.NullString
		stateStr      sql.NullString
		runFrameIDStr sql.NullString
	)
	err := ex.QueryRowContext(ctx,
		`SELECT id, state, frame_id
		   FROM rimsky_node_runs
		  WHERE node_id = ?
		    AND run_scope_id = ?
		    AND state IN ('pending','stale','running','held','parked')
		  ORDER BY CASE state
		             WHEN 'running' THEN 0
		             WHEN 'held' THEN 1
		             WHEN 'parked' THEN 2
		             WHEN 'stale' THEN 3
		             WHEN 'pending' THEN 4
		           END,
		           sequence ASC
		  LIMIT 1`,
		id.String(), runScopeID.String(),
	).Scan(&runIDStr, &stateStr, &runFrameIDStr)
	switch {
	case err == nil:
		if stateStr.Valid {
			current = cascade.NodeState(stateStr.String)
		}
	case errors.Is(err, sql.ErrNoRows):
		var failedScan sql.NullString
		fErr := ex.QueryRowContext(ctx,
			`SELECT state FROM rimsky_node_runs
			  WHERE node_id = ? AND state = 'failed'
			  ORDER BY COALESCE(active_terminal_at, enqueued_at) DESC
			  LIMIT 1`, id.String(),
		).Scan(&failedScan)
		switch {
		case fErr == nil && failedScan.Valid:
			current = cascade.NodeState(failedScan.String)
		case fErr == nil, errors.Is(fErr, sql.ErrNoRows):
		default:
			return fmt.Errorf("nodes.updateState: select failed run: %w", fErr)
		}
	default:
		return fmt.Errorf("nodes.updateState: select run row: %w", err)
	}
	var nodeFrameIDStr sql.NullString
	if err := ex.QueryRowContext(ctx,
		`SELECT frame_id FROM rimsky_nodes WHERE id = ?`, id.String(),
	).Scan(&nodeFrameIDStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("nodes.updateState: select node frame: %w", err)
	}
	expected, err := cascade.NextState(current, reason)
	if err != nil {
		return err
	}
	if expected != state {
		return foundationshared.Wrap(cascade.ErrIllegalTransition,
			"illegal state transition target",
			map[string]any{
				"id": id, "from": current, "requested": state,
				"computed": expected, "reason": reason.Kind,
			})
	}
	var settlingArg any
	if settlingSignalType == nil {
		settlingArg = nil
	} else {
		settlingArg = *settlingSignalType
	}
	if _, err := ex.ExecContext(ctx,
		`UPDATE rimsky_nodes
		   SET updated_at = ?,
		       frame_id = CASE WHEN ? = 'fresh' THEN NULL ELSE frame_id END
		 WHERE id = ?`,
		nowUTC(), string(state), id.String(),
	); err != nil {
		return fmt.Errorf("nodes.updateState: rimsky_nodes update: %w", err)
	}
	switch {
	case runIDStr.Valid:
		if _, err := ex.ExecContext(ctx,
			`UPDATE rimsky_node_runs
			   SET state = ?,
			       settling_signal_type = COALESCE(?, settling_signal_type)
			 WHERE id = ?`,
			string(state), settlingArg, runIDStr.String,
		); err != nil {
			return fmt.Errorf("nodes.updateState: run-row update: %w", err)
		}
	case state == cascade.NodeStateFresh:
	default:
		return fmt.Errorf("nodes.updateState: no in-flight run row for node %s on transition to %q (reason %q); use CreateNonCascadeStale or the cascade walker to create a new run", id, state, reason.Kind)
	}
	var progressFrame sql.NullString
	switch {
	case runFrameIDStr.Valid:
		progressFrame = runFrameIDStr
	default:
		progressFrame = nodeFrameIDStr
	}
	if progressFrame.Valid {
		if _, err := ex.ExecContext(ctx,
			`UPDATE rimsky_frames SET last_progress_at = ? WHERE frame_id = ?`,
			nowUTC(), progressFrame.String,
		); err != nil {
			return fmt.Errorf("nodes.updateState: refresh frame progress: %w", err)
		}
	}
	return nil
}

// @concept: error-policy
func (s *nodesImpl) UpdateRunEvaluatorState(ctx context.Context, runID foundationshared.UUID, es spec.EvaluatorState, tx persistence.Tx) error {
	_, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_node_runs SET retry_counter = ? WHERE id = ?`,
		es.RetryCounter, runID.String(),
	)
	return err
}

// @concept: error-policy
func (s *nodesImpl) GetRunEvaluatorState(ctx context.Context, runID foundationshared.UUID, tx persistence.Tx) (spec.EvaluatorState, error) {
	var es spec.EvaluatorState
	err := s.q(tx).QueryRowContext(ctx,
		`SELECT retry_counter FROM rimsky_node_runs WHERE id = ?`,
		runID.String(),
	).Scan(&es.RetryCounter)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return spec.EvaluatorState{}, nil
		}
		return spec.EvaluatorState{}, err
	}
	return es, nil
}

func (s *nodesImpl) SetFrameID(ctx context.Context, id foundationshared.UUID, frameID *foundationshared.UUID, tx persistence.Tx) error {
	_, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_nodes SET frame_id = ?, updated_at = ? WHERE id = ?`,
		nullableUUID(frameID), nowUTC(), id.String())
	return err
}

func (s *nodesImpl) ClearSettlingSignalType(ctx context.Context, id foundationshared.UUID, runScopeID foundationshared.UUID, tx persistence.Tx) error {
	if _, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_node_runs SET settling_signal_type = NULL
		  WHERE node_id = ?
		    AND run_scope_id = ?
		    AND state IN ('pending','stale','running','held','parked')`,
		id.String(), runScopeID.String()); err != nil {
		return err
	}
	_, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_nodes SET updated_at = ? WHERE id = ?`,
		nowUTC(), id.String())
	return err
}

func (s *nodesImpl) ResetFailedTerminalSettlingSignalType(ctx context.Context, id foundationshared.UUID, runScopeID foundationshared.UUID, tx persistence.Tx) error {
	var idStr string
	err := s.q(tx).QueryRowContext(ctx,
		`SELECT id FROM rimsky_node_runs
		  WHERE node_id = ? AND run_scope_id = ? AND state = 'failed'
		  ORDER BY COALESCE(active_terminal_at, enqueued_at) DESC
		  LIMIT 1`, id.String(), runScopeID.String(),
	).Scan(&idStr)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("nodes.ResetFailedTerminalSettlingSignalType: %w", err)
	}
	if _, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_node_runs SET settling_signal_type = NULL WHERE id = ?`,
		idStr,
	); err != nil {
		return fmt.Errorf("nodes.ResetFailedTerminalSettlingSignalType: %w", err)
	}
	_, err = s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_nodes SET updated_at = ? WHERE id = ?`,
		nowUTC(), id.String())
	return err
}

func (s *nodesImpl) GetFailedTerminalRunScopeID(ctx context.Context, id foundationshared.UUID, tx persistence.Tx) (*foundationshared.UUID, error) {
	var scopeStr string
	err := s.q(tx).QueryRowContext(ctx,
		`SELECT run_scope_id FROM rimsky_node_runs
		  WHERE node_id = ? AND state = 'failed'
		  ORDER BY COALESCE(active_terminal_at, enqueued_at) DESC
		  LIMIT 1`, id.String(),
	).Scan(&scopeStr)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("nodes.GetFailedTerminalRunScopeID: %w", err)
	}
	scope, err := uuid.Parse(scopeStr)
	if err != nil {
		return nil, fmt.Errorf("nodes.GetFailedTerminalRunScopeID: parse uuid: %w", err)
	}
	scopeUUID := foundationshared.UUID(scope)
	return &scopeUUID, nil
}

func (s *nodesImpl) DeleteByInstance(ctx context.Context, instanceID foundationshared.UUID, tx persistence.Tx) error {
	_, err := s.q(tx).ExecContext(ctx, `DELETE FROM rimsky_nodes WHERE instance_id = ?`, instanceID.String())
	return err
}

// @concept: cascade
// @decision: mode-default-most-recent
func (s *nodesImpl) GetCascadeMode(ctx context.Context, nodeID foundationshared.UUID, tx persistence.Tx) (cascade.CascadeMode, error) {
	var mode string
	err := s.q(tx).QueryRowContext(ctx,
		`SELECT cascade_mode FROM rimsky_nodes WHERE id = ?`, nodeID.String(),
	).Scan(&mode)
	if errors.Is(err, sql.ErrNoRows) {
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
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT state, count(*)
		   FROM rimsky_node_runs
		  WHERE node_id = ?
		  GROUP BY state`,
		nodeID.String(),
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

// @concept: signal
func (s *nodesImpl) HasRunForNodeInFrame(ctx context.Context, nodeID foundationshared.UUID, frameID foundationshared.UUID, tx persistence.Tx) (bool, error) {
	var n int
	err := s.q(tx).QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rimsky_node_runs WHERE node_id = ? AND frame_id = ?`,
		nodeID.String(), frameID.String(),
	).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("HasRunForNodeInFrame: %w", err)
	}
	return n > 0, nil
}

// @concept: cascade
// @concept: run-scope
func (s *nodesImpl) HasAdvancedSiblingInScope(
	ctx context.Context, tx persistence.Tx,
	nodeID, runScopeID, excludingRunID foundationshared.UUID,
) (bool, error) {
	var n int
	err := s.q(tx).QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rimsky_node_runs
		  WHERE node_id = ? AND run_scope_id = ? AND id <> ?
		    AND state IN ('stale', 'running', 'held', 'parked')`,
		nodeID.String(), runScopeID.String(), excludingRunID.String(),
	).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("HasAdvancedSiblingInScope: %w", err)
	}
	return n > 0, nil
}

// @concept: cascade
// @concept: run-scope
func (s *nodesImpl) ListPendingSiblingRunsInScope(
	ctx context.Context, tx persistence.Tx, senderRunID foundationshared.UUID,
) ([]foundationshared.UUID, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT pending.id
		   FROM rimsky_node_runs pending
		   JOIN rimsky_node_runs sender ON sender.id = ?
		  WHERE pending.node_id = sender.node_id
		    AND pending.run_scope_id = sender.run_scope_id
		    AND pending.id <> sender.id
		    AND pending.state = 'pending'
		  ORDER BY pending.sequence ASC`,
		senderRunID.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("ListPendingSiblingRunsInScope: %w", err)
	}
	defer rows.Close()
	var out []foundationshared.UUID
	for rows.Next() {
		var idStr string
		if err := rows.Scan(&idStr); err != nil {
			return nil, fmt.Errorf("ListPendingSiblingRunsInScope: scan: %w", err)
		}
		id, perr := uuid.Parse(idStr)
		if perr != nil {
			return nil, fmt.Errorf("ListPendingSiblingRunsInScope: parse id: %w", perr)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *nodesImpl) GetRunByDispatchIDForUpdate(ctx context.Context, dispatchID foundationshared.UUID, tx persistence.Tx) (*persistence.NodeRunForCallback, error) {
	var (
		r          persistence.NodeRunForCallback
		idStr      string
		nodeIDStr  string
		scopeIDStr string
		frameIDStr string
		stateStr   string
	)
	err := s.q(tx).QueryRowContext(ctx,
		`SELECT id, node_id, run_scope_id, frame_id, state
		   FROM rimsky_node_runs WHERE id = ?`, dispatchID.String(),
	).Scan(&idStr, &nodeIDStr, &scopeIDStr, &frameIDStr, &stateStr)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("GetRunByDispatchIDForUpdate: %w", err)
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return nil, fmt.Errorf("GetRunByDispatchIDForUpdate: id: %w", err)
	}
	nodeID, err := uuid.Parse(nodeIDStr)
	if err != nil {
		return nil, fmt.Errorf("GetRunByDispatchIDForUpdate: node_id: %w", err)
	}
	scopeID, err := uuid.Parse(scopeIDStr)
	if err != nil {
		return nil, fmt.Errorf("GetRunByDispatchIDForUpdate: run_scope_id: %w", err)
	}
	frameID, err := uuid.Parse(frameIDStr)
	if err != nil {
		return nil, fmt.Errorf("GetRunByDispatchIDForUpdate: frame_id: %w", err)
	}
	r.ID = id
	r.NodeID = nodeID
	r.RunScopeID = scopeID
	r.FrameID = frameID
	r.State = cascade.NodeState(stateStr)
	return &r, nil
}

func scanNode(sc scannable) (persistence.NodeRow, error) {
	var (
		r             persistence.NodeRow
		idStr         string
		instanceIDStr string
		executor      sql.NullString
		frameIDStr    sql.NullString
		tagsJSON      sql.NullString
		cascadeMode   sql.NullString
		createdAtStr  string
		updatedAtStr  string
	)
	if err := sc.Scan(
		&idStr, &instanceIDStr, &r.NodeType,
		&executor,
		&frameIDStr,
		&tagsJSON,
		&cascadeMode,
		&createdAtStr, &updatedAtStr,
	); err != nil {
		return persistence.NodeRow{}, err
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return persistence.NodeRow{}, fmt.Errorf("scanNode: id: %w", err)
	}
	instanceID, err := uuid.Parse(instanceIDStr)
	if err != nil {
		return persistence.NodeRow{}, fmt.Errorf("scanNode: instance_id: %w", err)
	}
	createdAt, err := parseTime(createdAtStr)
	if err != nil {
		return persistence.NodeRow{}, err
	}
	updatedAt, err := parseTime(updatedAtStr)
	if err != nil {
		return persistence.NodeRow{}, err
	}
	r.ID = id
	r.InstanceID = instanceID
	r.Executor = executor.String
	r.CreatedAt = createdAt
	r.UpdatedAt = updatedAt
	frameID, err := scanNullableUUID(frameIDStr)
	if err != nil {
		return persistence.NodeRow{}, err
	}
	r.FrameID = frameID
	tags, err := decodeTagsJSON(tagsJSON)
	if err != nil {
		return persistence.NodeRow{}, err
	}
	r.Tags = tags
	cm := cascadeMode.String
	if cm == "" {
		cm = string(cascade.CascadeModeMostRecent)
	}
	r.CascadeMode = cascade.CascadeMode(cm)
	return r, nil
}

func encodeTagsJSON(tags []string) (string, error) {
	if len(tags) == 0 {
		return "[]", nil
	}
	b, err := json.Marshal(tags)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func decodeTagsJSON(s sql.NullString) ([]string, error) {
	if !s.Valid || s.String == "" || s.String == "[]" {
		return []string{}, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s.String), &out); err != nil {
		return nil, fmt.Errorf("scanNode: tags: %w", err)
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}

func collectNodes(rows *sql.Rows) ([]persistence.NodeRow, error) {
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

// @concept: cascade
// @decision: walker-rule-per-sender-node
func (s *nodesImpl) LockReceiverCascade(
	_ context.Context, _ persistence.Tx, _, _, _ foundationshared.UUID,
) error {
	return nil
}

// @concept: cascade
// @decision: walker-rule-per-sender-node
func (s *nodesImpl) FindLatestCascadePending(
	ctx context.Context, tx persistence.Tx, nodeID, runScopeID, frameID foundationshared.UUID,
) (*persistence.NodeRunForGate, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT id, node_id, run_scope_id, frame_id, sequence, state, creation_reason, COALESCE(claimed_by, '')
		   FROM rimsky_node_runs
		  WHERE node_id = ? AND run_scope_id = ? AND frame_id = ?
		    AND state = 'pending'
		    AND creation_reason = 'cascade'
		  ORDER BY sequence DESC
		  LIMIT 1`,
		nodeID.String(), runScopeID.String(), frameID.String(),
	)
	return scanGateRow(row)
}

// @concept: cascade
// @decision: walker-rule-per-sender-node
func (s *nodesImpl) CreateCascadePending(
	ctx context.Context, tx persistence.Tx, nodeID, runScopeID, frameID foundationshared.UUID,
) (foundationshared.UUID, error) {
	newID := uuid.New()
	res, err := s.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_node_runs
		   (id, node_id, executor_name, required_stores, enqueued_at, state, creation_reason, sequence, frame_id, run_scope_id)
		 SELECT ?, n.id, NULLIF(n.executor, ''), '[]', ?, 'pending', 'cascade',
		        COALESCE((SELECT MAX(sequence) FROM rimsky_node_runs WHERE node_id = ? AND run_scope_id = ?), 0) + 1,
		        ?, rs.id
		   FROM rimsky_nodes n
		   JOIN rimsky_run_scopes rs ON rs.id = ? AND rs.closed_at IS NULL
		  WHERE n.id = ?`,
		newID.String(), nowUTC(),
		nodeID.String(), runScopeID.String(),
		frameID.String(),
		runScopeID.String(),
		nodeID.String(),
	)
	if err != nil {
		return foundationshared.UUID{}, fmt.Errorf("CreateCascadePending: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return foundationshared.UUID{}, fmt.Errorf("CreateCascadePending: rows affected: %w", err)
	}
	if affected == 0 {
		var closedAt sql.NullString
		serr := s.q(tx).QueryRowContext(ctx,
			`SELECT closed_at FROM rimsky_run_scopes WHERE id = ?`, runScopeID.String(),
		).Scan(&closedAt)
		if errors.Is(serr, sql.ErrNoRows) {
			return foundationshared.UUID{}, fmt.Errorf("CreateCascadePending: run scope %s not found", runScopeID)
		}
		if serr != nil {
			return foundationshared.UUID{}, fmt.Errorf("CreateCascadePending: lookup run scope: %w", serr)
		}
		if closedAt.Valid {
			return foundationshared.UUID{}, persistence.ErrRunScopeClosed
		}
		return foundationshared.UUID{}, fmt.Errorf("CreateCascadePending: node %s not found", nodeID)
	}
	if _, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_nodes SET frame_id = ?, updated_at = ? WHERE id = ?`,
		frameID.String(), nowUTC(), nodeID.String(),
	); err != nil {
		return foundationshared.UUID{}, fmt.Errorf("CreateCascadePending: bind node frame: %w", err)
	}
	return newID, nil
}

// @concept: cascade
func (s *nodesImpl) GetRunForGate(ctx context.Context, tx persistence.Tx, runID foundationshared.UUID) (*persistence.NodeRunForGate, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT id, node_id, run_scope_id, frame_id, sequence, state, creation_reason, COALESCE(claimed_by, '')
		   FROM rimsky_node_runs WHERE id = ?`, runID.String(),
	)
	return scanGateRow(row)
}

// @concept: node-run
func (s *nodesImpl) GetLatestRunForNode(
	ctx context.Context, tx persistence.Tx, nodeID foundationshared.UUID,
) (*persistence.NodeRunLatest, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT id, node_id, run_scope_id, frame_id, sequence, state,
		        settling_signal_type, COALESCE(claimed_by, '')
		   FROM rimsky_node_runs
		  WHERE node_id = ?
		  ORDER BY CASE WHEN state IN ('pending','stale','running','held','parked') THEN 0 ELSE 1 END,
		           sequence DESC
		  LIMIT 1`,
		nodeID.String(),
	)
	return scanLatestRow(row)
}

// @concept: node-run
func (s *nodesImpl) GetLatestRunInScope(
	ctx context.Context, tx persistence.Tx, nodeID, runScopeID foundationshared.UUID,
) (*persistence.NodeRunLatest, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT id, node_id, run_scope_id, frame_id, sequence, state,
		        settling_signal_type, COALESCE(claimed_by, '')
		   FROM rimsky_node_runs
		  WHERE node_id = ? AND run_scope_id = ?
		  ORDER BY CASE WHEN state IN ('pending','stale','running','held','parked') THEN 0 ELSE 1 END,
		           sequence DESC
		  LIMIT 1`,
		nodeID.String(), runScopeID.String(),
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
	placeholders := make([]string, 0, len(states))
	args := make([]any, 0, len(states)+1)
	args = append(args, instanceID.String())
	for _, st := range states {
		placeholders = append(placeholders, "?")
		args = append(args, string(st))
	}
	query := `SELECT r.id, r.node_id, r.run_scope_id, r.frame_id, r.sequence, r.state,
	                 r.settling_signal_type, COALESCE(r.claimed_by, '')
	            FROM rimsky_node_runs r
	            JOIN rimsky_nodes n ON n.id = r.node_id
	           WHERE n.instance_id = ? AND r.state IN (` + strings.Join(placeholders, ",") + `)
	           ORDER BY r.sequence ASC`
	rows, err := s.q(tx).QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("ListRunsForInstanceByStates: %w", err)
	}
	defer rows.Close()
	out := make([]persistence.NodeRunLatest, 0)
	for rows.Next() {
		var (
			r             persistence.NodeRunLatest
			runIDStr      string
			nodeIDStr     string
			runScopeIDStr string
			frameIDStr    string
			state         string
			sigType       sql.NullString
			claimedBy     string
		)
		if err := rows.Scan(&runIDStr, &nodeIDStr, &runScopeIDStr, &frameIDStr, &r.Sequence, &state, &sigType, &claimedBy); err != nil {
			return nil, fmt.Errorf("ListRunsForInstanceByStates: scan: %w", err)
		}
		if r.RunID, err = uuid.Parse(runIDStr); err != nil {
			return nil, fmt.Errorf("ListRunsForInstanceByStates: parse run_id: %w", err)
		}
		if r.NodeID, err = uuid.Parse(nodeIDStr); err != nil {
			return nil, fmt.Errorf("ListRunsForInstanceByStates: parse node_id: %w", err)
		}
		if r.RunScopeID, err = uuid.Parse(runScopeIDStr); err != nil {
			return nil, fmt.Errorf("ListRunsForInstanceByStates: parse run_scope_id: %w", err)
		}
		if r.FrameID, err = uuid.Parse(frameIDStr); err != nil {
			return nil, fmt.Errorf("ListRunsForInstanceByStates: parse frame_id: %w", err)
		}
		r.State = cascade.NodeState(state)
		if sigType.Valid {
			v := sigType.String
			r.SettlingSignalType = &v
		}
		r.ClaimedBy = claimedBy
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ListRunsForInstanceByStates: rows: %w", err)
	}
	return out, nil
}

func scanLatestRow(row *sql.Row) (*persistence.NodeRunLatest, error) {
	var (
		r             persistence.NodeRunLatest
		runIDStr      string
		nodeIDStr     string
		runScopeIDStr string
		frameIDStr    string
		state         string
		sigType       sql.NullString
		claimedBy     string
	)
	err := row.Scan(&runIDStr, &nodeIDStr, &runScopeIDStr, &frameIDStr, &r.Sequence, &state, &sigType, &claimedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scanLatestRow: %w", err)
	}
	r.RunID, err = uuid.Parse(runIDStr)
	if err != nil {
		return nil, err
	}
	r.NodeID, err = uuid.Parse(nodeIDStr)
	if err != nil {
		return nil, err
	}
	r.RunScopeID, err = uuid.Parse(runScopeIDStr)
	if err != nil {
		return nil, err
	}
	r.FrameID, err = uuid.Parse(frameIDStr)
	if err != nil {
		return nil, err
	}
	r.State = cascade.NodeState(state)
	if sigType.Valid {
		v := sigType.String
		r.SettlingSignalType = &v
	}
	r.ClaimedBy = claimedBy
	return &r, nil
}

// @concept: cascade
func (s *nodesImpl) GetPriorRunBySequence(
	ctx context.Context, tx persistence.Tx, nodeID, runScopeID foundationshared.UUID, beforeSeq int64,
) (*persistence.NodeRunForGate, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT id, node_id, run_scope_id, frame_id, sequence, state, creation_reason, COALESCE(claimed_by, '')
		   FROM rimsky_node_runs
		  WHERE node_id = ? AND run_scope_id = ? AND sequence < ?
		  ORDER BY sequence DESC
		  LIMIT 1`,
		nodeID.String(), runScopeID.String(), beforeSeq,
	)
	return scanGateRow(row)
}

// @concept: cascade
// @decision: mode-default-most-recent
func (s *nodesImpl) DeletePriorCascadeStales(
	ctx context.Context, tx persistence.Tx, nodeID, runScopeID foundationshared.UUID, beforeSeq int64,
) (int, error) {
	res, err := s.q(tx).ExecContext(ctx,
		`DELETE FROM rimsky_node_runs
		  WHERE node_id = ? AND run_scope_id = ? AND sequence < ?
		    AND state = 'stale' AND creation_reason = 'cascade' AND claimed_by IS NULL`,
		nodeID.String(), runScopeID.String(), beforeSeq,
	)
	if err != nil {
		return 0, fmt.Errorf("DeletePriorCascadeStales: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("DeletePriorCascadeStales: rows affected: %w", err)
	}
	return int(n), nil
}

// @concept: cascade
func (s *nodesImpl) GetPriorCascadeStaleNotClaimed(
	ctx context.Context, tx persistence.Tx, nodeID, runScopeID foundationshared.UUID, beforeSeq int64,
) (*persistence.NodeRunForGate, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT id, node_id, run_scope_id, frame_id, sequence, state, creation_reason, COALESCE(claimed_by, '')
		   FROM rimsky_node_runs
		  WHERE node_id = ? AND run_scope_id = ? AND sequence < ?
		    AND state = 'stale' AND creation_reason = 'cascade' AND claimed_by IS NULL
		  ORDER BY sequence DESC
		  LIMIT 1`,
		nodeID.String(), runScopeID.String(), beforeSeq,
	)
	return scanGateRow(row)
}

// @concept: cascade
func (s *nodesImpl) GetMostRecentSettledRun(
	ctx context.Context, tx persistence.Tx, nodeID, runScopeID foundationshared.UUID, beforeSeq int64,
) (*persistence.NodeRunForGate, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT id, node_id, run_scope_id, frame_id, sequence, state, creation_reason, COALESCE(claimed_by, '')
		   FROM rimsky_node_runs
		  WHERE node_id = ? AND run_scope_id = ? AND sequence < ?
		    AND state = 'fresh'
		  ORDER BY sequence DESC
		  LIMIT 1`,
		nodeID.String(), runScopeID.String(), beforeSeq,
	)
	return scanGateRow(row)
}

// @concept: cascade
func (s *nodesImpl) DropPendingRun(
	ctx context.Context, tx persistence.Tx, runID foundationshared.UUID,
) error {
	res, err := s.q(tx).ExecContext(ctx,
		`DELETE FROM rimsky_node_runs WHERE id = ? AND state = 'pending'`, runID.String(),
	)
	if err != nil {
		return fmt.Errorf("DropPendingRun: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("DropPendingRun: rows affected: %w", err)
	}
	if n == 0 {
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
	res, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_node_runs
		    SET state = ?, enqueued_at = ?
		  WHERE id = ? AND state = 'pending'`,
		string(target), formatTime(enqueuedAt), runID.String(),
	)
	if err != nil {
		return fmt.Errorf("TransitionPendingToStale: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("TransitionPendingToStale: rows affected: %w", err)
	}
	if n == 0 {
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
	storesJSON := marshalStringArray(in.RequiredClaimProducers)
	var priorID any
	if in.PriorDispatchID != nil {
		priorID = in.PriorDispatchID.String()
	}
	var scratchInline any
	if len(in.InitialScratchInline) > 0 {
		scratchInline = in.InitialScratchInline
	}
	newID := uuid.New()
	res, err := s.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_node_runs
		   (id, node_id, executor_name, required_stores, enqueued_at, state, creation_reason, sequence,
		    frame_id, run_scope_id, prior_dispatch_id, prior_dispatch_disposition,
		    scratch_inline, scratch_handle, scratch_handle_backend)
		 SELECT ?, ?, ?, ?, ?, 'stale', ?,
		        COALESCE((SELECT MAX(sequence) FROM rimsky_node_runs WHERE node_id = ? AND run_scope_id = ?), 0) + 1,
		        ?, rs.id, ?, ?, ?, ?, ?
		   FROM rimsky_run_scopes rs
		  WHERE rs.id = ? AND rs.closed_at IS NULL`,
		newID.String(), in.NodeID.String(), nullableString(in.ExecutorName), storesJSON, formatTime(in.EnqueuedAt), string(in.CreationReason),
		in.NodeID.String(), in.RunScopeID.String(),
		in.FrameID.String(),
		priorID, nullableString(in.PriorDispatchDisposition),
		scratchInline, nullableString(in.InitialScratchHandle), nullableString(in.InitialScratchHandleBackend),
		in.RunScopeID.String(),
	)
	if err != nil {
		return foundationshared.UUID{}, fmt.Errorf("CreateNonCascadeStale: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return foundationshared.UUID{}, fmt.Errorf("CreateNonCascadeStale: rows affected: %w", err)
	}
	if n == 0 {
		var closedAt sql.NullString
		serr := s.q(tx).QueryRowContext(ctx,
			`SELECT closed_at FROM rimsky_run_scopes WHERE id = ?`, in.RunScopeID.String(),
		).Scan(&closedAt)
		if errors.Is(serr, sql.ErrNoRows) {
			return foundationshared.UUID{}, fmt.Errorf("CreateNonCascadeStale: run scope %s not found", in.RunScopeID)
		}
		if serr != nil {
			return foundationshared.UUID{}, fmt.Errorf("CreateNonCascadeStale: lookup run scope: %w", serr)
		}
		if closedAt.Valid {
			return foundationshared.UUID{}, persistence.ErrRunScopeClosed
		}
		return foundationshared.UUID{}, fmt.Errorf("CreateNonCascadeStale: insert affected zero rows")
	}
	// @decision: non-cascade-direct-to-stale
	if err := (*nodeAttributesImpl)((*tablesImpl)(s)).SnapshotBagForNewRun(ctx, tx, newID, in.NodeID, in.RunScopeID); err != nil {
		return foundationshared.UUID{}, fmt.Errorf("CreateNonCascadeStale: snapshot bag: %w", err)
	}
	return newID, nil
}

func scanGateRow(row *sql.Row) (*persistence.NodeRunForGate, error) {
	var (
		runIDStr     string
		nodeIDStr    string
		runScopeStr  string
		frameIDStr   string
		sequence     int64
		stateStr     string
		reasonStr    string
		claimedByStr string
	)
	err := row.Scan(&runIDStr, &nodeIDStr, &runScopeStr, &frameIDStr, &sequence, &stateStr, &reasonStr, &claimedByStr)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scanGateRow: %w", err)
	}
	runID, err := parseUUID(runIDStr)
	if err != nil {
		return nil, fmt.Errorf("scanGateRow: run_id: %w", err)
	}
	nodeID, err := parseUUID(nodeIDStr)
	if err != nil {
		return nil, fmt.Errorf("scanGateRow: node_id: %w", err)
	}
	runScope, err := parseUUID(runScopeStr)
	if err != nil {
		return nil, fmt.Errorf("scanGateRow: run_scope_id: %w", err)
	}
	frameID, err := parseUUID(frameIDStr)
	if err != nil {
		return nil, fmt.Errorf("scanGateRow: frame_id: %w", err)
	}
	return &persistence.NodeRunForGate{
		RunID:          runID,
		NodeID:         nodeID,
		RunScopeID:     runScope,
		FrameID:        frameID,
		Sequence:       sequence,
		State:          cascade.NodeState(stateStr),
		CreationReason: cascade.CreationReason(reasonStr),
		ClaimedBy:      claimedByStr,
	}, nil
}
