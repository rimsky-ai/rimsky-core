// nodes.go — SQLite-backed persistence.NodeStore.
//
// @blessed-invariant 1: state machine rejects illegal transitions.
// UpdateState never short-circuits on from==to; the node-state machine
// alone decides legality.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	nodepkg "github.com/fallguy/rimsky/modeling/node"
	"github.com/fallguy/rimsky/modeling/shared"
)

const nodeCols = `
  id, instance_id, node_type, executor, schedule_cron, state, dependencies,
  current_error_class, retry_counter, action_index, last_heartbeat_at,
  assigned_supervisor_id, frame_id, created_at, updated_at
`

func (s *nodesImpl) Create(ctx context.Context, in persistence.NodeCreateInput, tx persistence.Tx) (persistence.NodeRow, error) {
	deps := in.Dependencies
	if deps == nil {
		deps = []shared.UUID{}
	}
	now := nowUTC()
	row := s.q(tx).QueryRowContext(ctx,
		`INSERT INTO rimsky_nodes (
		   id, instance_id, node_type, executor, schedule_cron, state, dependencies, created_at, updated_at
		 ) VALUES (?, ?, ?, ?, ?, 'fresh', ?, ?, ?)
		 RETURNING `+nodeCols,
		in.ID.String(), in.InstanceID.String(), in.NodeType,
		nullableString(in.Executor), nullableString(in.ScheduleCron),
		marshalUUIDArray(deps), now, now,
	)
	return scanNode(row)
}

func (s *nodesImpl) Get(ctx context.Context, id shared.UUID, tx persistence.Tx) (*persistence.NodeRow, error) {
	row := s.q(tx).QueryRowContext(ctx,
		`SELECT `+nodeCols+` FROM rimsky_nodes WHERE id = ?`, id.String())
	out, err := scanNode(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

func (s *nodesImpl) ListByInstance(ctx context.Context, instanceID shared.UUID, tx persistence.Tx) ([]persistence.NodeRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+nodeCols+` FROM rimsky_nodes
		 WHERE instance_id = ?
		 ORDER BY created_at ASC`, instanceID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectNodes(rows)
}

func (s *nodesImpl) ListByInstancePaged(
	ctx context.Context,
	instanceID shared.UUID,
	pag persistence.ListPagination,
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
		`SELECT `+nodeCols+` FROM rimsky_nodes
		 WHERE instance_id = ?
		   AND (
		     ? IS NULL
		     OR (created_at, id) > (
		       (SELECT created_at FROM rimsky_nodes WHERE id = ?),
		       ?
		     )
		   )
		 ORDER BY created_at ASC, id ASC
		 LIMIT ?`,
		instanceID.String(), cursor, cursor, cursor, limit,
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

// ListReadyForDispatch returns executor-backed nodes in 'stale' whose
// dependencies are all 'fresh' and that have no outstanding dispatch row.
//
// SQLite has no `unnest()` or `ANY(array)`; we use json_each() over the
// JSON-array dependencies column instead.
func (s *nodesImpl) ListReadyForDispatch(ctx context.Context, tx persistence.Tx) ([]persistence.NodeRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+nodeCols+` FROM rimsky_nodes n
		 WHERE n.executor IS NOT NULL AND n.executor <> ''
		   AND n.state = 'stale'
		   AND NOT EXISTS (
		     SELECT 1 FROM json_each(n.dependencies) je
		     JOIN rimsky_nodes d ON d.id = je.value
		     WHERE d.state <> 'fresh'
		   )
		   AND NOT EXISTS (
		     SELECT 1 FROM rimsky_dispatch x WHERE x.node_id = n.id
		   )
		 ORDER BY n.created_at ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectNodes(rows)
}

func (s *nodesImpl) ListPureCascadeReady(ctx context.Context, tx persistence.Tx) ([]persistence.NodeRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+nodeCols+` FROM rimsky_nodes n
		 WHERE (n.executor IS NULL OR n.executor = '')
		   AND n.state = 'stale'
		   AND NOT EXISTS (
		     SELECT 1 FROM json_each(n.dependencies) je
		     JOIN rimsky_nodes d ON d.id = je.value
		     WHERE d.state <> 'fresh'
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
		`SELECT `+nodeCols+` FROM rimsky_nodes
		 WHERE state = 'running' ORDER BY updated_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectNodes(rows)
}

// ListDependentsOf finds nodes that include nodeID in their JSON-array
// dependencies column. SQLite has no ANY() so we json_each the column
// against a literal id.
func (s *nodesImpl) ListDependentsOf(ctx context.Context, nodeID shared.UUID, tx persistence.Tx) ([]persistence.NodeRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+nodeCols+` FROM rimsky_nodes n
		 WHERE EXISTS (
		   SELECT 1 FROM json_each(n.dependencies) je WHERE je.value = ?
		 )
		 ORDER BY n.created_at ASC`, nodeID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectNodes(rows)
}

func (s *nodesImpl) ListWithStaleHeartbeat(ctx context.Context, cutoff time.Time, tx persistence.Tx) ([]persistence.NodeRow, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT `+nodeCols+` FROM rimsky_nodes
		 WHERE state = 'running'
		   AND last_heartbeat_at IS NOT NULL
		   AND last_heartbeat_at < ?`, formatTime(cutoff))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectNodes(rows)
}

func (s *nodesImpl) CountByState(ctx context.Context, tx persistence.Tx) (map[shared.NodeState]int, error) {
	rows, err := s.q(tx).QueryContext(ctx,
		`SELECT state, count(*) FROM rimsky_nodes GROUP BY state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[shared.NodeState]int{
		shared.NodeStateFresh:   0,
		shared.NodeStateStale:   0,
		shared.NodeStateRunning: 0,
		shared.NodeStateFailed:  0,
	}
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			return nil, err
		}
		out[shared.NodeState(state)] = count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateState enforces the node state machine on every call, mirroring
// the postgres impl. SQLite has no SELECT FOR UPDATE; the surrounding
// BEGIN IMMEDIATE writer-slot hold serialises the SELECT+UPDATE atomically.
func (s *nodesImpl) UpdateState(
	ctx context.Context,
	id shared.UUID,
	state shared.NodeState,
	reason cascade.TransitionReason,
	tx persistence.Tx,
) error {
	if tx != nil {
		return s.enforceAndUpdate(ctx, s.q(tx), id, state, reason)
	}
	sTx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = sTx.Rollback() }()
	if err := s.enforceAndUpdate(ctx, sTx, id, state, reason); err != nil {
		return err
	}
	return sTx.Commit()
}

func (s *nodesImpl) enforceAndUpdate(
	ctx context.Context,
	ex querier,
	id shared.UUID,
	state shared.NodeState,
	reason cascade.TransitionReason,
) error {
	var current shared.NodeState
	err := ex.QueryRowContext(ctx,
		`SELECT state FROM rimsky_nodes WHERE id = ?`, id.String(),
	).Scan(&current)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("nodes.updateState: select: %w", err)
	}
	expected, err := cascade.NextState(current, reason)
	if err != nil {
		return err
	}
	if expected != state {
		return shared.Wrap(shared.ErrIllegalTransition,
			"illegal state transition target",
			map[string]any{
				"id": id, "from": current, "requested": state,
				"computed": expected, "reason": reason.Kind,
			})
	}
	_, err = ex.ExecContext(ctx,
		`UPDATE rimsky_nodes
		   SET state = ?,
		       updated_at = ?,
		       assigned_supervisor_id = CASE WHEN ? = 'running' THEN assigned_supervisor_id ELSE NULL END,
		       last_heartbeat_at = CASE WHEN ? = 'running' THEN last_heartbeat_at ELSE NULL END,
		       frame_id = CASE WHEN ? = 'fresh' THEN NULL ELSE frame_id END
		 WHERE id = ?`,
		string(state), nowUTC(), string(state), string(state), string(state), id.String(),
	)
	return err
}

func (s *nodesImpl) UpdateError(ctx context.Context, id shared.UUID, es nodepkg.EvaluatorState, tx persistence.Tx) error {
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

func (s *nodesImpl) UpdateHeartbeat(ctx context.Context, id shared.UUID, at time.Time, supervisorID string, tx persistence.Tx) error {
	_, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_nodes
		   SET last_heartbeat_at = ?,
		       assigned_supervisor_id = COALESCE(?, assigned_supervisor_id)
		 WHERE id = ?`,
		formatTime(at), nullableString(supervisorID), id.String(),
	)
	return err
}

func (s *nodesImpl) SetFrameID(ctx context.Context, id shared.UUID, frameID *shared.UUID, tx persistence.Tx) error {
	_, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_nodes SET frame_id = ?, updated_at = ? WHERE id = ?`,
		nullableUUID(frameID), nowUTC(), id.String())
	return err
}

func (s *nodesImpl) ClearSupervisorAssignment(ctx context.Context, id shared.UUID, tx persistence.Tx) error {
	_, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_nodes
		   SET assigned_supervisor_id = NULL,
		       last_heartbeat_at = NULL
		 WHERE id = ?`, id.String())
	return err
}

func (s *nodesImpl) DeleteByInstance(ctx context.Context, instanceID shared.UUID, tx persistence.Tx) error {
	_, err := s.q(tx).ExecContext(ctx, `DELETE FROM rimsky_nodes WHERE instance_id = ?`, instanceID.String())
	return err
}

func (s *nodesImpl) MarkStaleForCascade(ctx context.Context, id shared.UUID, frameID shared.UUID, tx persistence.Tx) error {
	_, err := s.q(tx).ExecContext(ctx,
		`UPDATE rimsky_nodes
		   SET state = 'stale', frame_id = ?, updated_at = ?
		 WHERE id = ?
		   AND (state = 'fresh' OR (state = 'stale' AND frame_id IS NULL))`,
		frameID.String(), nowUTC(), id.String(),
	)
	if err != nil {
		return fmt.Errorf("nodesImpl.MarkStaleForCascade: %w", err)
	}
	return nil
}

func scanNode(sc scannable) (persistence.NodeRow, error) {
	var (
		r               persistence.NodeRow
		idStr           string
		instanceIDStr   string
		executor        sql.NullString
		scheduleCron    sql.NullString
		stateStr        string
		dependenciesStr string
		currentErrClass sql.NullString
		lastHB          sql.NullString
		assignedSup     sql.NullString
		frameIDStr      sql.NullString
		createdAtStr    string
		updatedAtStr    string
	)
	if err := sc.Scan(
		&idStr, &instanceIDStr, &r.NodeType,
		&executor, &scheduleCron, &stateStr,
		&dependenciesStr,
		&currentErrClass, &r.RetryCounter, &r.ActionIndex,
		&lastHB, &assignedSup, &frameIDStr,
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
	deps, err := unmarshalUUIDArray(dependenciesStr)
	if err != nil {
		return persistence.NodeRow{}, err
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
	r.State = shared.NodeState(stateStr)
	r.Dependencies = deps
	r.Executor = executor.String
	r.ScheduleCron = scheduleCron.String
	r.CurrentErrorClass = currentErrClass.String
	r.AssignedSupervisorID = assignedSup.String
	r.CreatedAt = createdAt
	r.UpdatedAt = updatedAt
	if lastHB.Valid {
		t, err := parseTime(lastHB.String)
		if err != nil {
			return persistence.NodeRow{}, err
		}
		r.LastHeartbeatAt = &t
	}
	frameID, err := scanNullableUUID(frameIDStr)
	if err != nil {
		return persistence.NodeRow{}, err
	}
	r.FrameID = frameID
	return r, nil
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
