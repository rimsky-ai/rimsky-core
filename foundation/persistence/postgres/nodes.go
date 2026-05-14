// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// NodeTable — port of rimsky/src/storage/postgres/cell-store.ts, renamed for
// the cell→node rename (spec §11.1). The `kind` discriminator is gone; the
// store manages `executor` and `schedule_cron` nullable TEXT columns instead.
//
// @blessed-invariant (§17): UpdateState never short-circuits on from==to.
// Every UpdateState call runs cascade.NextState(current, reason); identical
// from/to under e.g. dispatch_claimed must reject as illegal, not silently
// succeed. This is the load-bearing guard against double-execute.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/fallguy/rimsky/foundation/cascade"
	"github.com/fallguy/rimsky/foundation/persistence"
	foundationshared "github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/foundation/spec"
)

const nodeCols = `
  id, instance_id, node_type, executor, schedule_cron, state, last_outcome,
  current_error_class, retry_counter, action_index, last_heartbeat_at,
  assigned_supervisor_id, frame_id, created_at, updated_at
`

// Create inserts a new node row with state='fresh'. Under the frame
// resolution model (docs/history/2026-04-26-frame-resolution-design.md
// §3.1), nodes transition fresh→stale only via frame-start (or via
// cascade message-pass during a running frame). Pre-frame-resolution
// the default was 'stale' because the scheduler's ready sweep was the
// initial-run trigger; under frames the trigger is the
// frame.EnqueueOrCoalesce + scheduler-tick frame engine pair.
func (s *nodesImpl) Create(ctx context.Context, in persistence.NodeCreateInput, tx persistence.Tx) (persistence.NodeRow, error) {
	ex := s.q(tx)
	row := ex.QueryRow(ctx,
		`INSERT INTO rimsky_nodes (
		   id, instance_id, node_type, executor, schedule_cron, state
		 ) VALUES ($1, $2, $3, $4, $5, 'fresh')
		 RETURNING `+nodeCols,
		in.ID, in.InstanceID, in.NodeType,
		nullableString(in.Executor), nullableString(in.ScheduleCron),
	)
	return scanNode(row)
}

func (s *nodesImpl) Get(ctx context.Context, id foundationshared.UUID, tx persistence.Tx) (*persistence.NodeRow, error) {
	ex := s.q(tx)
	row := ex.QueryRow(ctx,
		`SELECT `+nodeCols+` FROM rimsky_nodes WHERE id = $1`, id)
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
		`SELECT `+nodeCols+` FROM rimsky_nodes
		 WHERE instance_id = $1
		 ORDER BY created_at ASC`, instanceID)
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
		`SELECT `+nodeCols+` FROM rimsky_nodes
		 WHERE instance_id = $1
		   AND (
		     $2::uuid IS NULL
		     OR (created_at, id) > (
		       (SELECT created_at FROM rimsky_nodes WHERE id = $2::uuid),
		       $2::uuid
		     )
		   )
		 ORDER BY created_at ASC, id ASC
		 LIMIT $3`,
		instanceID, cursor, limit,
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

// ListReadyForDispatch returns executor-backed nodes in `stale` whose
// wait-set is empty in their current frame and that have no
// outstanding dispatch row.
//
// Post-2026-05-14 the eligibility predicate is wait-set-empty rather
// than dependencies-all-fresh; the cascade walk populates
// rimsky_wait_set on every sender transition and the settled-state
// drain removes rows when senders resolve.
//
//	@concept: wait-set
func (s *nodesImpl) ListReadyForDispatch(ctx context.Context, tx persistence.Tx) ([]persistence.NodeRow, error) {
	ex := s.q(tx)
	rows, err := ex.Query(ctx,
		`SELECT `+nodeCols+` FROM rimsky_nodes n
		 WHERE n.executor IS NOT NULL AND n.executor <> ''
		   AND n.state = 'stale'
		   AND NOT EXISTS (
		     SELECT 1 FROM rimsky_wait_set w
		     WHERE w.frame_id = n.frame_id AND w.receiver_node_id = n.id
		   )
		   AND NOT EXISTS (
		     SELECT 1 FROM rimsky_node_runs x WHERE x.node_id = n.id
		   )
		 ORDER BY n.created_at ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectNodes(rows)
}

// ListPureCascadeReady returns pure-cascade (Executor == "") nodes in state
// 'stale' whose wait-set is empty in their current frame. The
// scheduler's cascade sweeper uses this list to flip them to 'fresh'
// directly without going through the dispatch queue.
//
//	@concept: wait-set
func (s *nodesImpl) ListPureCascadeReady(ctx context.Context, tx persistence.Tx) ([]persistence.NodeRow, error) {
	ex := s.q(tx)
	rows, err := ex.Query(ctx,
		`SELECT `+nodeCols+` FROM rimsky_nodes n
		 WHERE (n.executor IS NULL OR n.executor = '')
		   AND n.state = 'stale'
		   AND NOT EXISTS (
		     SELECT 1 FROM rimsky_wait_set w
		     WHERE w.frame_id = n.frame_id AND w.receiver_node_id = n.id
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
		`SELECT `+nodeCols+` FROM rimsky_nodes
		 WHERE state = 'running' ORDER BY updated_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectNodes(rows)
}

func (s *nodesImpl) ListRunningBySupervisor(ctx context.Context, supervisorID string, tx persistence.Tx) ([]persistence.NodeRow, error) {
	ex := s.q(tx)
	rows, err := ex.Query(ctx,
		`SELECT `+nodeCols+` FROM rimsky_nodes
		 WHERE state = 'running' AND assigned_supervisor_id = $1
		 ORDER BY updated_at ASC`, supervisorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectNodes(rows)
}

func (s *nodesImpl) ListWithStaleHeartbeat(ctx context.Context, cutoff time.Time, tx persistence.Tx) ([]persistence.NodeRow, error) {
	ex := s.q(tx)
	rows, err := ex.Query(ctx,
		`SELECT `+nodeCols+` FROM rimsky_nodes
		 WHERE state = 'running'
		   AND last_heartbeat_at IS NOT NULL
		   AND last_heartbeat_at < $1`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectNodes(rows)
}

func (s *nodesImpl) CountByState(ctx context.Context, tx persistence.Tx) (map[cascade.NodeState]int, error) {
	ex := s.q(tx)
	rows, err := ex.Query(ctx,
		`SELECT state, count(*)::int FROM rimsky_nodes GROUP BY state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[cascade.NodeState]int{
		cascade.NodeStateFresh:   0,
		cascade.NodeStateStale:   0,
		cascade.NodeStateRunning: 0,
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

// UpdateState enforces the node state machine on every call. Takes a row
// lock for the duration of the transition so two concurrent updaters can't
// compute conflicting next-states.
//
// `lastOutcome` is the resolution flavor for terminal-for-this-frame
// transitions; the empty string "" means "do not write the column"
// (preserves the existing value via COALESCE).
func (s *nodesImpl) UpdateState(
	ctx context.Context,
	id foundationshared.UUID,
	state cascade.NodeState,
	reason cascade.TransitionReason,
	lastOutcome cascade.LastOutcome,
	tx persistence.Tx,
) error {
	return s.enforceAndUpdate(ctx, s.q(tx), id, state, reason, lastOutcome)
}

func (s *nodesImpl) enforceAndUpdate(
	ctx context.Context,
	ex querier,
	id foundationshared.UUID,
	state cascade.NodeState,
	reason cascade.TransitionReason,
	lastOutcome cascade.LastOutcome,
) error {
	var current cascade.NodeState
	var frameIDBefore *foundationshared.UUID
	err := ex.QueryRow(ctx,
		`SELECT state, frame_id FROM rimsky_nodes WHERE id = $1 FOR UPDATE`, id,
	).Scan(&current, &frameIDBefore)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// TS parity: silent no-op when row gone.
			return nil
		}
		return fmt.Errorf("nodes.updateState: select: %w", err)
	}
	// @blessed-invariant (§17): no short-circuit; state machine alone decides.
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
	// Defensive frame_id clear on every transition to 'fresh' (spec §4.4 +
	// §10.3): a fresh node carries no frame_id. Centralising the clear
	// here means producers don't have to issue a separate SetFrameID call
	// — and a producer that forgets cannot strand a stale frame_id on a
	// fresh row (per review Issue 8 / Issue 24).
	//
	// last_outcome is preserved when the caller passes "" (empty); explicit
	// values overwrite. COALESCE($3::text, last_outcome) leaves the
	// column unchanged on transitions like → stale or → running where
	// the resolution flavor is not yet known.
	var outcomeArg any
	if lastOutcome == "" {
		outcomeArg = nil
	} else {
		outcomeArg = string(lastOutcome)
	}
	_, err = ex.Exec(ctx,
		`UPDATE rimsky_nodes
		   SET state = $2,
		       updated_at = NOW(),
		       assigned_supervisor_id = CASE WHEN $2 = 'running' THEN assigned_supervisor_id ELSE NULL END,
		       last_heartbeat_at = CASE WHEN $2 = 'running' THEN last_heartbeat_at ELSE NULL END,
		       frame_id = CASE WHEN $2 = 'fresh' THEN NULL ELSE frame_id END,
		       last_outcome = COALESCE($3::text, last_outcome)
		 WHERE id = $1`,
		id, string(state), outcomeArg,
	)
	if err != nil {
		return err
	}
	// Refresh frame progress: any state-transition write within a frame
	// counts as progress for frame_timeout_ms's "no progress in window"
	// semantics. The frame_id captured BEFORE the UPDATE is the closing
	// frame for transitions to 'fresh' (which clear frame_id); using the
	// before-value lets the closing frame record progress.
	if frameIDBefore != nil {
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

func (s *nodesImpl) UpdateHeartbeat(ctx context.Context, id foundationshared.UUID, at time.Time, supervisorID string, tx persistence.Tx) error {
	ex := s.q(tx)
	_, err := ex.Exec(ctx,
		`UPDATE rimsky_nodes
		   SET last_heartbeat_at = $2,
		       assigned_supervisor_id = COALESCE($3, assigned_supervisor_id)
		 WHERE id = $1`,
		id, at, nullableString(supervisorID),
	)
	return err
}

// SetFrameID writes (or clears) the frame_id column on a node. Used by the
// scheduler's frame engine at frame-start (write) and the supervisor's
// terminal-commit path on success (clear). Per spec §10.3.
func (s *nodesImpl) SetFrameID(ctx context.Context, id foundationshared.UUID, frameID *foundationshared.UUID, tx persistence.Tx) error {
	ex := s.q(tx)
	_, err := ex.Exec(ctx,
		`UPDATE rimsky_nodes SET frame_id = $2, updated_at = NOW() WHERE id = $1`, id, frameID)
	return err
}

// ClearLastOutcome resets last_outcome to NULL on the row. Used by the
// operator reset path (control/controlapi/nodes.go::handleResetNode)
// so the dashboard does not display a stale `failed` resolution flavor
// while the node transitions back through stale → running → fresh.
func (s *nodesImpl) ClearLastOutcome(ctx context.Context, id foundationshared.UUID, tx persistence.Tx) error {
	ex := s.q(tx)
	_, err := ex.Exec(ctx,
		`UPDATE rimsky_nodes SET last_outcome = NULL, updated_at = NOW() WHERE id = $1`, id)
	return err
}

func (s *nodesImpl) ClearSupervisorAssignment(ctx context.Context, id foundationshared.UUID, tx persistence.Tx) error {
	ex := s.q(tx)
	_, err := ex.Exec(ctx,
		`UPDATE rimsky_nodes
		   SET assigned_supervisor_id = NULL,
		       last_heartbeat_at = NULL
		 WHERE id = $1`, id)
	return err
}

func (s *nodesImpl) DeleteByInstance(ctx context.Context, instanceID foundationshared.UUID, tx persistence.Tx) error {
	ex := s.q(tx)
	_, err := ex.Exec(ctx, `DELETE FROM rimsky_nodes WHERE instance_id = $1`, instanceID)
	return err
}

// MarkStaleForCascade is the parent-commit cascade target. Sets
// state='stale', frame_id=$1 only when the row is currently fresh OR
// (stale AND frame_id IS NULL). Used by the supervisor's terminal-
// complete path so cascade children inherit the parent's frame_id
// atomically with the commit.
func (s *nodesImpl) MarkStaleForCascade(ctx context.Context, id foundationshared.UUID, frameID foundationshared.UUID, tx persistence.Tx) error {
	ex := s.q(tx)
	_, err := ex.Exec(ctx,
		`UPDATE rimsky_nodes
		   SET state = 'stale', frame_id = $1, updated_at = now()
		 WHERE id = $2
		   AND (state = 'fresh' OR (state = 'stale' AND frame_id IS NULL))`,
		frameID, id,
	)
	if err != nil {
		return fmt.Errorf("nodesImpl.MarkStaleForCascade: %w", err)
	}
	return nil
}

// ---- helpers ----

func scanNode(sc scannable) (persistence.NodeRow, error) {
	var (
		r               persistence.NodeRow
		executor        *string
		scheduleCron    *string
		lastOutcome     *string
		currentErrClass *string
		lastHB          *time.Time
		assignedSup     *string
		frameID         *foundationshared.UUID
	)
	if err := sc.Scan(
		&r.ID, &r.InstanceID, &r.NodeType,
		&executor, &scheduleCron, &r.State, &lastOutcome,
		&currentErrClass, &r.RetryCounter, &r.ActionIndex,
		&lastHB, &assignedSup, &frameID,
		&r.CreatedAt, &r.UpdatedAt,
	); err != nil {
		return persistence.NodeRow{}, err
	}
	r.Executor = derefString(executor)
	r.ScheduleCron = derefString(scheduleCron)
	if lastOutcome != nil {
		r.LastOutcome = cascade.LastOutcome(*lastOutcome)
	}
	r.CurrentErrorClass = derefString(currentErrClass)
	r.AssignedSupervisorID = derefString(assignedSup)
	r.LastHeartbeatAt = lastHB
	r.FrameID = frameID
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
