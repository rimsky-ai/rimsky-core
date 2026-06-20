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

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	foundationshared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

const nodeCols = `
  n.id, n.instance_id, n.node_type, n.executor,
  COALESCE(r.state, 'fresh') AS state, r.settling_signal_type,
  n.current_error_class, n.retry_counter, n.action_index,
  r.claimed_by AS assigned_supervisor_id,
  n.frame_id, n.tags, n.created_at, n.updated_at,
  CASE WHEN r.phase IN ('pending','active','held','parked') THEN r.id END AS in_flight_run_id,
  CASE WHEN r.phase IN ('pending','active','held','parked') THEN r.run_scope_id END AS in_flight_run_scope_id
`

const nodeSelect = `FROM rimsky_nodes n
LEFT JOIN (
    SELECT id, node_id, state, settling_signal_type, claimed_by, frame_id, phase, run_scope_id,
           ROW_NUMBER() OVER (
               PARTITION BY node_id
               ORDER BY CASE WHEN phase IN ('pending','active','held','parked') THEN 0 ELSE 1 END,
                        COALESCE(active_terminal_at, enqueued_at) DESC
           ) AS rk
      FROM rimsky_node_runs
) r ON r.node_id = n.id AND r.rk = 1`

func (s *nodesImpl) Create(ctx context.Context, in persistence.NodeCreateInput, tx persistence.Tx) (persistence.NodeRow, error) {
	now := nowUTC()
	tagsJSON, terr := encodeTagsJSON(in.Tags)
	if terr != nil {
		return persistence.NodeRow{}, fmt.Errorf("nodes.Create: encode tags: %w", terr)
	}
	if _, err := s.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_nodes (
		   id, instance_id, node_type, executor, tags, created_at, updated_at
		 ) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		in.ID.String(), in.InstanceID.String(), in.NodeType,
		nullableString(in.Executor),
		tagsJSON,
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
		`SELECT `+nodeCols+` `+nodeSelect+`
		 WHERE n.executor IS NOT NULL AND n.executor <> ''
		   AND r.state IN ('stale','resuming')
		   AND r.phase = 'pending'
		   AND NOT EXISTS (
		     SELECT 1 FROM rimsky_wait_set w
		     WHERE w.frame_id = n.frame_id AND w.receiver_run_id = r.id
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
func (s *nodesImpl) ListPureCascadeReady(ctx context.Context, tx persistence.Tx) ([]persistence.NodeRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+nodeCols+` `+nodeSelect+`
		 WHERE (n.executor IS NULL OR n.executor = '')
		   AND r.state = 'stale'
		   AND NOT EXISTS (
		     SELECT 1 FROM rimsky_wait_set w
		     WHERE w.frame_id = n.frame_id AND w.receiver_run_id = r.id
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

func (s *nodesImpl) ListRunning(ctx context.Context, tx persistence.Tx) ([]persistence.NodeRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+nodeCols+` `+nodeSelect+`
		  WHERE r.state = 'running'
		  ORDER BY n.updated_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectNodes(rows)
}

func (s *nodesImpl) CountByState(ctx context.Context, tx persistence.Tx) (map[cascade.NodeState]int, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT COALESCE(r.state, 'fresh') AS s, count(*)
		 `+nodeSelect+`
		 GROUP BY COALESCE(r.state, 'fresh')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[cascade.NodeState]int{
		cascade.NodeStateFresh:    0,
		cascade.NodeStateStale:    0,
		cascade.NodeStateRunning:  0,
		cascade.NodeStateFailed:   0,
		cascade.NodeStateParked:   0,
		cascade.NodeStateResuming: 0,
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
		    AND phase IN ('pending','active','held','parked')`,
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
			  WHERE node_id = ? AND phase = 'failed'
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
	case state == cascade.NodeStateStale && (current == cascade.NodeStateFailed || current == cascade.NodeStateFresh):
		if nodeFrameIDStr.Valid {
			if _, err := ex.ExecContext(ctx,
				`INSERT INTO rimsky_node_runs
				   (id, node_id, executor_name, required_stores, enqueued_at, phase, state, settling_signal_type, frame_id, run_scope_id)
				 SELECT ?, n.id, n.executor, '[]', ?, 'pending', 'stale', ?, ?, ?
				   FROM rimsky_nodes n
				  WHERE n.id = ?
				    AND NOT EXISTS (
				      SELECT 1 FROM rimsky_node_runs r
				       WHERE r.node_id = ?
				         AND r.run_scope_id = ?
				         AND r.phase IN ('pending','active','held','parked')
				    )`,
				uuid.New().String(), nowUTC(), settlingArg,
				nodeFrameIDStr.String, runScopeID.String(), id.String(), id.String(), runScopeID.String(),
			); err != nil {
				return fmt.Errorf("nodes.updateState: seed stale run-row: %w", err)
			}
		}
	default:
		return fmt.Errorf("nodes.updateState: no in-flight run row for node %s on transition to %q (reason %q)", id, state, reason.Kind)
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

func (s *nodesImpl) UpdateError(ctx context.Context, id foundationshared.UUID, es spec.EvaluatorState, tx persistence.Tx) error {
	_, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_nodes
		   SET action_index = ?,
		       retry_counter = ?,
		       current_error_class = ?,
		       updated_at = ?
		 WHERE id = ?`,
		es.ActionIndex, es.RetryCounter, nullableString(es.CurrentErrorClass), nowUTC(), id.String(),
	)
	return err
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
		    AND phase IN ('pending','active','held','parked')`,
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
		  WHERE node_id = ? AND run_scope_id = ? AND phase = 'failed'
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
		  WHERE node_id = ? AND phase = 'failed'
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
func (s *nodesImpl) MarkStaleForCascade(ctx context.Context, runID foundationshared.UUID, frameID foundationshared.UUID, tx persistence.Tx) error {
	res, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_node_runs
		   SET state = 'stale', frame_id = ?
		 WHERE id = ?
		   AND phase IN ('pending','active','held','parked')`,
		frameID.String(), runID.String(),
	)
	if err != nil {
		return fmt.Errorf("nodesImpl.MarkStaleForCascade: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	if _, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_nodes SET frame_id = ?, updated_at = ?
		  WHERE id = (SELECT node_id FROM rimsky_node_runs WHERE id = ?)`,
		frameID.String(), nowUTC(), runID.String(),
	); err != nil {
		return fmt.Errorf("nodesImpl.MarkStaleForCascade: bind frame: %w", err)
	}
	return nil
}

// @concept: run-scope
func (s *nodesImpl) AffirmNodeRunRow(ctx context.Context, nodeID foundationshared.UUID, runScopeID foundationshared.UUID, frameID foundationshared.UUID, tx persistence.Tx) error {
	res, err := s.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_node_runs
		   (id, node_id, executor_name, required_stores, enqueued_at, phase, state, frame_id, run_scope_id)
		 SELECT ?,
		        n.id,
		        NULLIF(n.executor, ''),
		        COALESCE((
		          SELECT json_group_array(json_extract(store.value, '$.name'))
		            FROM rimsky_instances i
		            JOIN rimsky_templates t ON t.id = i.template_hash
		            JOIN json_each(t.spec, '$.nodes') AS nd
		            JOIN json_each(nd.value, '$.stores') AS store
		           WHERE i.id = n.instance_id
		             AND json_extract(nd.value, '$.type') = n.node_type
		        ), '[]'),
		        ?, 'pending', 'stale', ?, rs.id
		   FROM rimsky_nodes n
		   JOIN rimsky_run_scopes rs ON rs.id = ? AND rs.closed_at IS NULL
		  WHERE n.id = ?
		    AND NOT EXISTS (
		      SELECT 1 FROM rimsky_node_runs r
		       WHERE r.node_id = ?
		         AND r.run_scope_id = ?
		         AND r.phase IN ('pending','active','held','parked')
		    )`,
		uuid.New().String(), nowUTC(), frameID.String(),
		runScopeID.String(),
		nodeID.String(), nodeID.String(), runScopeID.String(),
	)
	if err != nil {
		return fmt.Errorf("AffirmNodeRunRow: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("AffirmNodeRunRow: rows affected: %w", err)
	}
	if affected > 0 {
		return nil
	}
	var closedAt sql.NullString
	err = s.q(tx).QueryRowContext(ctx,
		`SELECT closed_at FROM rimsky_run_scopes WHERE id = ?`, runScopeID.String(),
	).Scan(&closedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("AffirmNodeRunRow: run scope %s not found", runScopeID)
	}
	if err != nil {
		return fmt.Errorf("AffirmNodeRunRow: lookup run scope: %w", err)
	}
	if closedAt.Valid {
		return persistence.ErrRunScopeClosed
	}
	return nil
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

func (s *nodesImpl) GetRunByDispatchIDForUpdate(ctx context.Context, dispatchID foundationshared.UUID, tx persistence.Tx) (*persistence.NodeRunForCallback, error) {
	var (
		r          persistence.NodeRunForCallback
		idStr      string
		nodeIDStr  string
		scopeIDStr string
		frameIDStr string
		phase      string
		stateStr   string
	)
	err := s.q(tx).QueryRowContext(ctx,
		`SELECT id, node_id, run_scope_id, frame_id, phase, state
		   FROM rimsky_node_runs WHERE id = ?`, dispatchID.String(),
	).Scan(&idStr, &nodeIDStr, &scopeIDStr, &frameIDStr, &phase, &stateStr)
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
	r.Phase = phase
	r.State = cascade.NodeState(stateStr)
	return &r, nil
}

func scanNode(sc scannable) (persistence.NodeRow, error) {
	var (
		r                 persistence.NodeRow
		idStr             string
		instanceIDStr     string
		executor          sql.NullString
		stateStr          string
		settlingSignalStr sql.NullString
		currentErrClass   sql.NullString
		assignedSup       sql.NullString
		frameIDStr        sql.NullString
		tagsJSON          sql.NullString
		createdAtStr      string
		updatedAtStr      string
		inFlightRunIDStr  sql.NullString
		inFlightScopeStr  sql.NullString
	)
	if err := sc.Scan(
		&idStr, &instanceIDStr, &r.NodeType,
		&executor, &stateStr, &settlingSignalStr,
		&currentErrClass, &r.RetryCounter, &r.ActionIndex,
		&assignedSup, &frameIDStr,
		&tagsJSON,
		&createdAtStr, &updatedAtStr,
		&inFlightRunIDStr, &inFlightScopeStr,
	); err != nil {
		return persistence.NodeRow{}, err
	}
	if settlingSignalStr.Valid {
		v := settlingSignalStr.String
		r.SettlingSignalType = &v
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
	r.State = cascade.NodeState(stateStr)
	r.Executor = executor.String
	r.CurrentErrorClass = currentErrClass.String
	r.AssignedSupervisorID = assignedSup.String
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
	runID, err := scanNullableUUID(inFlightRunIDStr)
	if err != nil {
		return persistence.NodeRow{}, err
	}
	r.InFlightRunID = runID
	runScope, err := scanNullableUUID(inFlightScopeStr)
	if err != nil {
		return persistence.NodeRow{}, err
	}
	r.RunScopeID = runScope
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
