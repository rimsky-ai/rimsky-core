// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package postgres

import (
	"context"
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
  COALESCE(r.state, 'fresh') AS state, r.settling_signal_type,
  n.current_error_class, n.retry_counter, n.action_index,
  r.claimed_by AS assigned_supervisor_id,
  n.frame_id, n.tags, n.created_at, n.updated_at,
  CASE WHEN r.phase IN ('pending','active','held','parked') THEN r.id END AS in_flight_run_id,
  CASE WHEN r.phase IN ('pending','active','held','parked') THEN r.run_scope_id END AS in_flight_run_scope_id
`

const nodeSelect = `FROM rimsky_nodes n
LEFT JOIN LATERAL (
    SELECT id, state, settling_signal_type, claimed_by, frame_id, phase, run_scope_id
      FROM rimsky_node_runs
     WHERE node_id = n.id
     ORDER BY CASE WHEN phase IN ('pending','active','held','parked') THEN 0 ELSE 1 END,
              COALESCE(active_terminal_at, enqueued_at) DESC
     LIMIT 1
) r ON true`

func (s *nodesImpl) Create(ctx context.Context, in persistence.NodeCreateInput, tx persistence.Tx) (persistence.NodeRow, error) {
	ex := s.q(tx)
	tags := in.Tags
	if tags == nil {
		tags = []string{}
	}
	if _, err := ex.Exec(ctx,
		`INSERT INTO rimsky_nodes (
		   id, instance_id, node_type, executor, tags
		 ) VALUES ($1, $2, $3, $4, $5)`,
		in.ID, in.InstanceID, in.NodeType,
		nullableString(in.Executor),
		tags,
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
	ex := s.q(tx)
	rows, err := ex.Query(ctx,
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
	ex := s.q(tx)
	rows, err := ex.Query(ctx,
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
	ex := s.q(tx)
	rows, err := ex.Query(ctx,
		`SELECT COALESCE(r.state, 'fresh') AS state, count(*)::int
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
	var (
		current       cascade.NodeState = cascade.NodeStateFresh
		runID         *foundationshared.UUID
		runFrameID    *foundationshared.UUID
		frameIDBefore *foundationshared.UUID
	)
	var (
		runIDScan      *foundationshared.UUID
		stateScan      *string
		runFrameIDScan *foundationshared.UUID
	)
	err := ex.QueryRow(ctx,
		`SELECT id, state, frame_id
		   FROM rimsky_node_runs
		  WHERE node_id = $1
		    AND run_scope_id = $2
		    AND phase IN ('pending','active','held','parked')
		  FOR UPDATE`, id, runScopeID,
	).Scan(&runIDScan, &stateScan, &runFrameIDScan)
	switch {
	case err == nil:
		runID = runIDScan
		if stateScan != nil {
			current = cascade.NodeState(*stateScan)
		}
		runFrameID = runFrameIDScan
	case errors.Is(err, pgx.ErrNoRows):
		var failedScan *string
		if err := ex.QueryRow(ctx,
			`SELECT state FROM rimsky_node_runs
			  WHERE node_id = $1 AND phase = 'failed'
			  ORDER BY COALESCE(active_terminal_at, enqueued_at) DESC
			  LIMIT 1`, id,
		).Scan(&failedScan); err == nil && failedScan != nil {
			current = cascade.NodeState(*failedScan)
		} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("nodes.updateState: select failed run: %w", err)
		}
	default:
		return fmt.Errorf("nodes.updateState: select run row: %w", err)
	}
	if err := ex.QueryRow(ctx,
		`SELECT frame_id FROM rimsky_nodes WHERE id = $1`, id,
	).Scan(&frameIDBefore); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
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
	if _, err := ex.Exec(ctx,
		`UPDATE rimsky_nodes
		   SET updated_at = NOW(),
		       frame_id = CASE WHEN $2 = 'fresh' THEN NULL ELSE frame_id END
		 WHERE id = $1`,
		id, string(state),
	); err != nil {
		return fmt.Errorf("nodes.updateState: rimsky_nodes update: %w", err)
	}
	switch {
	case runID != nil:
		if _, err := ex.Exec(ctx,
			`UPDATE rimsky_node_runs
			   SET state = $2,
			       settling_signal_type = COALESCE($3::text, settling_signal_type)
			 WHERE id = $1`,
			*runID, string(state), settlingArg,
		); err != nil {
			return fmt.Errorf("nodes.updateState: run-row update: %w", err)
		}
	case state == cascade.NodeStateFresh:
	case state == cascade.NodeStateStale && (current == cascade.NodeStateFailed || current == cascade.NodeStateFresh):
		if frameIDBefore != nil {
			if _, err := ex.Exec(ctx,
				`INSERT INTO rimsky_node_runs
				   (id, node_id, executor_name, required_stores, enqueued_at, phase, state, settling_signal_type, frame_id, run_scope_id)
				 SELECT gen_random_uuid(), n.id, n.executor, ARRAY[]::text[], NOW(), 'pending', 'stale', $3::text, $2, $4
				   FROM rimsky_nodes n
				  WHERE n.id = $1
				    AND NOT EXISTS (
				      SELECT 1 FROM rimsky_node_runs r
				       WHERE r.node_id = $1
				         AND r.run_scope_id = $4
				         AND r.phase IN ('pending','active','held','parked')
				    )`,
				id, *frameIDBefore, settlingArg, runScopeID,
			); err != nil {
				return fmt.Errorf("nodes.updateState: seed stale run-row: %w", err)
			}
		}
	default:
		return fmt.Errorf("nodes.updateState: no in-flight run row for node %s on transition to %q (reason %q)", id, state, reason.Kind)
	}
	if runFrameID != nil {
		if _, err := ex.Exec(ctx,
			`UPDATE rimsky_frames SET last_progress_at = NOW() WHERE frame_id = $1`,
			*runFrameID,
		); err != nil {
			return fmt.Errorf("nodes.updateState: refresh frame progress: %w", err)
		}
	} else if frameIDBefore != nil {
		if _, err := ex.Exec(ctx,
			`UPDATE rimsky_frames SET last_progress_at = NOW() WHERE frame_id = $1`,
			*frameIDBefore,
		); err != nil {
			return fmt.Errorf("nodes.updateState: refresh frame progress: %w", err)
		}
	}
	return nil
}

func (s *nodesImpl) UpdateError(ctx context.Context, id foundationshared.UUID, es spec.EvaluatorState, tx persistence.Tx) error {
	ex := s.q(tx)
	_, err := ex.Exec(ctx,
		`UPDATE rimsky_nodes
		   SET action_index = $2,
		       retry_counter = $3,
		       current_error_class = $4,
		       updated_at = NOW()
		 WHERE id = $1`,
		id, es.ActionIndex, es.RetryCounter, nullableString(es.CurrentErrorClass),
	)
	return err
}

func (s *nodesImpl) SetFrameID(ctx context.Context, id foundationshared.UUID, frameID *foundationshared.UUID, tx persistence.Tx) error {
	ex := s.q(tx)
	_, err := ex.Exec(ctx,
		`UPDATE rimsky_nodes SET frame_id = $2, updated_at = NOW() WHERE id = $1`, id, frameID)
	return err
}

func (s *nodesImpl) ClearSettlingSignalType(ctx context.Context, id foundationshared.UUID, runScopeID foundationshared.UUID, tx persistence.Tx) error {
	ex := s.q(tx)
	if _, err := ex.Exec(ctx,
		`UPDATE rimsky_node_runs SET settling_signal_type = NULL
		  WHERE node_id = $1
		    AND run_scope_id = $2
		    AND phase IN ('pending','active','held','parked')`, id, runScopeID); err != nil {
		return err
	}
	_, err := ex.Exec(ctx,
		`UPDATE rimsky_nodes SET updated_at = NOW() WHERE id = $1`, id)
	return err
}

func (s *nodesImpl) ResetFailedTerminalSettlingSignalType(ctx context.Context, id foundationshared.UUID, runScopeID foundationshared.UUID, tx persistence.Tx) error {
	ex := s.q(tx)
	tag, err := ex.Exec(ctx,
		`WITH target AS (
		     SELECT id
		       FROM rimsky_node_runs
		      WHERE node_id = $1 AND run_scope_id = $2 AND phase = 'failed'
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
	if tag.RowsAffected() == 0 {
		return nil
	}
	_, err = ex.Exec(ctx,
		`UPDATE rimsky_nodes SET updated_at = NOW() WHERE id = $1`, id)
	return err
}

func (s *nodesImpl) GetFailedTerminalRunScopeID(ctx context.Context, id foundationshared.UUID, tx persistence.Tx) (*foundationshared.UUID, error) {
	ex := s.q(tx)
	var scope foundationshared.UUID
	err := ex.QueryRow(ctx,
		`SELECT run_scope_id FROM rimsky_node_runs
		  WHERE node_id = $1 AND phase = 'failed'
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

// @concept: cascade
func (s *nodesImpl) MarkStaleForCascade(ctx context.Context, runID foundationshared.UUID, frameID foundationshared.UUID, tx persistence.Tx) error {
	ex := s.q(tx)
	tag, err := ex.Exec(ctx,
		`UPDATE rimsky_node_runs
		   SET state = 'stale', frame_id = $2
		 WHERE id = $1
		   AND phase IN ('pending','active','held','parked')`,
		runID, frameID,
	)
	if err != nil {
		return fmt.Errorf("nodesImpl.MarkStaleForCascade: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	if _, err := ex.Exec(ctx,
		`UPDATE rimsky_nodes SET frame_id = $1, updated_at = now()
		  WHERE id = (SELECT node_id FROM rimsky_node_runs WHERE id = $2)`,
		frameID, runID,
	); err != nil {
		return fmt.Errorf("nodesImpl.MarkStaleForCascade: bind frame: %w", err)
	}
	return nil
}

// @concept: run-scope
func (s *nodesImpl) AffirmNodeRunRow(ctx context.Context, nodeID foundationshared.UUID, runScopeID foundationshared.UUID, frameID foundationshared.UUID, tx persistence.Tx) error {
	ex := s.q(tx)
	tag, err := ex.Exec(ctx,
		`INSERT INTO rimsky_node_runs
		   (id, node_id, executor_name, required_stores, enqueued_at, phase, state, frame_id, run_scope_id)
		 SELECT gen_random_uuid(), n.id,
		        NULLIF(n.executor, '') AS executor_name,
		        COALESCE((
		          SELECT array_agg(store->>'name')
		            FROM rimsky_instances i
		            JOIN rimsky_templates t ON t.id = i.template_hash
		            CROSS JOIN LATERAL jsonb_array_elements(t.spec->'nodes') AS nd
		            LEFT JOIN LATERAL jsonb_array_elements(nd->'stores') AS store ON true
		           WHERE i.id = n.instance_id
		             AND nd->>'type' = n.node_type
		             AND store IS NOT NULL
		        ), ARRAY[]::text[]) AS required_stores,
		        NOW(), 'pending', 'stale', $3, $2
		   FROM rimsky_nodes n
		   JOIN rimsky_run_scopes rs ON rs.id = $2 AND rs.closed_at IS NULL
		  WHERE n.id = $1
		    AND NOT EXISTS (
		      SELECT 1 FROM rimsky_node_runs r
		       WHERE r.node_id = $1
		         AND r.run_scope_id = $2
		         AND r.phase IN ('pending','active','held','parked')
		    )`,
		nodeID, runScopeID, frameID,
	)
	if err != nil {
		return fmt.Errorf("AffirmNodeRunRow: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	var closedAt *time.Time
	err = ex.QueryRow(ctx, `SELECT closed_at FROM rimsky_run_scopes WHERE id = $1`, runScopeID).Scan(&closedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("AffirmNodeRunRow: run scope %s not found", runScopeID)
	}
	if err != nil {
		return fmt.Errorf("AffirmNodeRunRow: lookup run scope: %w", err)
	}
	if closedAt != nil {
		return persistence.ErrRunScopeClosed
	}
	return nil
}

// @concept: signal
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

func (s *nodesImpl) GetRunByDispatchIDForUpdate(ctx context.Context, dispatchID foundationshared.UUID, tx persistence.Tx) (*persistence.NodeRunForCallback, error) {
	ex := s.q(tx)
	var (
		r         persistence.NodeRunForCallback
		stateScan string
	)
	err := ex.QueryRow(ctx,
		`SELECT id, node_id, run_scope_id, frame_id, phase, state
		   FROM rimsky_node_runs
		  WHERE id = $1
		  FOR UPDATE`, dispatchID,
	).Scan(&r.ID, &r.NodeID, &r.RunScopeID, &r.FrameID, &r.Phase, &stateScan)
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
		r                  persistence.NodeRow
		executor           *string
		settlingSignalType *string
		currentErrClass    *string
		assignedSup        *string
		frameID            *foundationshared.UUID
		tags               []string
		inFlightRunID      *foundationshared.UUID
		inFlightRunScope   *foundationshared.UUID
	)
	if err := sc.Scan(
		&r.ID, &r.InstanceID, &r.NodeType,
		&executor, &r.State, &settlingSignalType,
		&currentErrClass, &r.RetryCounter, &r.ActionIndex,
		&assignedSup, &frameID,
		&tags,
		&r.CreatedAt, &r.UpdatedAt,
		&inFlightRunID, &inFlightRunScope,
	); err != nil {
		return persistence.NodeRow{}, err
	}
	r.InFlightRunID = inFlightRunID
	r.RunScopeID = inFlightRunScope
	r.Executor = derefString(executor)
	r.SettlingSignalType = settlingSignalType
	r.CurrentErrorClass = derefString(currentErrClass)
	r.AssignedSupervisorID = derefString(assignedSup)
	r.FrameID = frameID
	if tags == nil {
		tags = []string{}
	}
	r.Tags = tags
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
