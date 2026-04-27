// NodeStore — port of rimsky/src/storage/postgres/cell-store.ts, renamed for
// the cell→node rename (spec §11.1). The `kind` discriminator is gone; the
// store manages `executor` and `schedule_cron` nullable TEXT columns instead.
//
// @blessed-invariant (§17): UpdateState never short-circuits on from==to.
// Every UpdateState call runs node.NextState(current, reason); identical
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
	"github.com/jackc/pgx/v5/pgxpool"

	nodepkg "github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
)

type NodeStore struct {
	pool *pgxpool.Pool
}

var _ storage.NodeStore = (*NodeStore)(nil)

const nodeCols = `
  id, instance_id, node_type, executor, schedule_cron, state, dependencies,
  current_error_class, retry_counter, action_index, last_heartbeat_at,
  assigned_supervisor_id, frame_id, created_at, updated_at
`

// Create inserts a new node row with state='fresh'. Under the frame
// resolution model (docs/specs/2026-04-26-frame-resolution-design.md
// §3.1), nodes transition fresh→stale only via frame-start (or via
// cascade message-pass during a running frame). Pre-frame-resolution
// the default was 'stale' because the scheduler's ready sweep was the
// initial-run trigger; under frames the trigger is the
// frame.EnqueueOrCoalesce + scheduler-tick frame engine pair.
func (s *NodeStore) Create(ctx context.Context, in storage.NodeCreateInput, tx storage.Tx) (storage.NodeRow, error) {
	ex := q(tx, s.pool)
	deps := in.Dependencies
	if deps == nil {
		deps = []shared.UUID{}
	}
	row := ex.QueryRow(ctx,
		`INSERT INTO rimsky_nodes (
		   id, instance_id, node_type, executor, schedule_cron, state, dependencies
		 ) VALUES ($1, $2, $3, $4, $5, 'fresh', $6)
		 RETURNING `+nodeCols,
		in.ID, in.InstanceID, in.NodeType,
		nullableString(in.Executor), nullableString(in.ScheduleCron),
		deps,
	)
	return scanNode(row)
}

func (s *NodeStore) Get(ctx context.Context, id shared.UUID, tx storage.Tx) (*storage.NodeRow, error) {
	ex := q(tx, s.pool)
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

func (s *NodeStore) ListByInstance(ctx context.Context, instanceID shared.UUID, tx storage.Tx) ([]storage.NodeRow, error) {
	ex := q(tx, s.pool)
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

func (s *NodeStore) ListByInstancePaged(
	ctx context.Context,
	instanceID shared.UUID,
	pag storage.ListPagination,
	tx storage.Tx,
) (storage.PaginatedListResult[storage.NodeRow], error) {
	ex := q(tx, s.pool)
	limit := pag.Limit
	if limit <= 0 {
		limit = 100
	}
	var cursor *shared.UUID
	if pag.Cursor != "" {
		u, err := uuid.Parse(pag.Cursor)
		if err != nil {
			return storage.PaginatedListResult[storage.NodeRow]{}, fmt.Errorf("nodes.listByInstancePaged: bad cursor: %w", err)
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
		return storage.PaginatedListResult[storage.NodeRow]{}, err
	}
	defer rows.Close()
	out, err := collectNodes(rows)
	if err != nil {
		return storage.PaginatedListResult[storage.NodeRow]{}, err
	}
	var nextCursor string
	if len(out) == limit && len(out) > 0 {
		nextCursor = out[len(out)-1].ID.String()
	}
	return storage.PaginatedListResult[storage.NodeRow]{Rows: out, NextCursor: nextCursor}, nil
}

// ListReadyForDispatch returns executor-backed nodes in `stale` whose
// dependencies are all `fresh` and that have no outstanding dispatch row.
func (s *NodeStore) ListReadyForDispatch(ctx context.Context, tx storage.Tx) ([]storage.NodeRow, error) {
	ex := q(tx, s.pool)
	rows, err := ex.Query(ctx,
		`SELECT `+nodeCols+` FROM rimsky_nodes n
		 WHERE n.executor IS NOT NULL AND n.executor <> ''
		   AND n.state = 'stale'
		   AND NOT EXISTS (
		     SELECT 1 FROM unnest(n.dependencies) AS dep_id
		     JOIN rimsky_nodes d ON d.id = dep_id
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

// ListPureCascadeReady returns pure-cascade (Executor == "") nodes in state
// 'stale' whose deps are all 'fresh'. The scheduler's cascade sweeper uses
// this list to flip them to 'fresh' directly without going through the
// dispatch queue.
func (s *NodeStore) ListPureCascadeReady(ctx context.Context, tx storage.Tx) ([]storage.NodeRow, error) {
	ex := q(tx, s.pool)
	rows, err := ex.Query(ctx,
		`SELECT `+nodeCols+` FROM rimsky_nodes n
		 WHERE (n.executor IS NULL OR n.executor = '')
		   AND n.state = 'stale'
		   AND NOT EXISTS (
		     SELECT 1 FROM unnest(n.dependencies) AS dep_id
		     JOIN rimsky_nodes d ON d.id = dep_id
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

func (s *NodeStore) ListRunning(ctx context.Context, tx storage.Tx) ([]storage.NodeRow, error) {
	ex := q(tx, s.pool)
	rows, err := ex.Query(ctx,
		`SELECT `+nodeCols+` FROM rimsky_nodes
		 WHERE state = 'running' ORDER BY updated_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectNodes(rows)
}

func (s *NodeStore) ListDependentsOf(ctx context.Context, nodeID shared.UUID, tx storage.Tx) ([]storage.NodeRow, error) {
	ex := q(tx, s.pool)
	rows, err := ex.Query(ctx,
		`SELECT `+nodeCols+` FROM rimsky_nodes
		 WHERE $1 = ANY(dependencies) ORDER BY created_at ASC`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectNodes(rows)
}

func (s *NodeStore) ListWithStaleHeartbeat(ctx context.Context, cutoff time.Time, tx storage.Tx) ([]storage.NodeRow, error) {
	ex := q(tx, s.pool)
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

func (s *NodeStore) CountByState(ctx context.Context, tx storage.Tx) (map[shared.NodeState]int, error) {
	ex := q(tx, s.pool)
	rows, err := ex.Query(ctx,
		`SELECT state, count(*)::int FROM rimsky_nodes GROUP BY state`)
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

// UpdateState enforces the node state machine on every call. Takes a row
// lock for the duration of the transition so two concurrent updaters can't
// compute conflicting next-states.
func (s *NodeStore) UpdateState(
	ctx context.Context,
	id shared.UUID,
	state shared.NodeState,
	reason nodepkg.TransitionReason,
	tx storage.Tx,
) error {
	if tx != nil {
		return s.enforceAndUpdate(ctx, q(tx, s.pool), id, state, reason)
	}
	// Open an ephemeral tx so the SELECT FOR UPDATE + UPDATE are atomic.
	pgT, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = pgT.Rollback(ctx) }()
	if err := s.enforceAndUpdate(ctx, pgT, id, state, reason); err != nil {
		return err
	}
	return pgT.Commit(ctx)
}

func (s *NodeStore) enforceAndUpdate(
	ctx context.Context,
	ex querier,
	id shared.UUID,
	state shared.NodeState,
	reason nodepkg.TransitionReason,
) error {
	var current shared.NodeState
	err := ex.QueryRow(ctx,
		`SELECT state FROM rimsky_nodes WHERE id = $1 FOR UPDATE`, id,
	).Scan(&current)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// TS parity: silent no-op when row gone.
			return nil
		}
		return fmt.Errorf("nodes.updateState: select: %w", err)
	}
	// @blessed-invariant (§17): no short-circuit; state machine alone decides.
	expected, err := nodepkg.NextState(current, reason)
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
	// Defensive frame_id clear on every transition to 'fresh' (spec §4.4 +
	// §10.3): a fresh node carries no frame_id. Centralising the clear
	// here means producers don't have to issue a separate SetFrameID call
	// — and a producer that forgets cannot strand a stale frame_id on a
	// fresh row (per review Issue 8 / Issue 24).
	_, err = ex.Exec(ctx,
		`UPDATE rimsky_nodes
		   SET state = $2,
		       updated_at = NOW(),
		       assigned_supervisor_id = CASE WHEN $2 = 'running' THEN assigned_supervisor_id ELSE NULL END,
		       last_heartbeat_at = CASE WHEN $2 = 'running' THEN last_heartbeat_at ELSE NULL END,
		       frame_id = CASE WHEN $2 = 'fresh' THEN NULL ELSE frame_id END
		 WHERE id = $1`,
		id, string(state),
	)
	return err
}

func (s *NodeStore) UpdateError(ctx context.Context, id shared.UUID, es nodepkg.EvaluatorState, tx storage.Tx) error {
	ex := q(tx, s.pool)
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

func (s *NodeStore) UpdateHeartbeat(ctx context.Context, id shared.UUID, at time.Time, supervisorID string, tx storage.Tx) error {
	ex := q(tx, s.pool)
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
func (s *NodeStore) SetFrameID(ctx context.Context, id shared.UUID, frameID *shared.UUID, tx storage.Tx) error {
	ex := q(tx, s.pool)
	_, err := ex.Exec(ctx,
		`UPDATE rimsky_nodes SET frame_id = $2, updated_at = NOW() WHERE id = $1`, id, frameID)
	return err
}

func (s *NodeStore) ClearSupervisorAssignment(ctx context.Context, id shared.UUID, tx storage.Tx) error {
	ex := q(tx, s.pool)
	_, err := ex.Exec(ctx,
		`UPDATE rimsky_nodes
		   SET assigned_supervisor_id = NULL,
		       last_heartbeat_at = NULL
		 WHERE id = $1`, id)
	return err
}

func (s *NodeStore) DeleteByInstance(ctx context.Context, instanceID shared.UUID, tx storage.Tx) error {
	ex := q(tx, s.pool)
	_, err := ex.Exec(ctx, `DELETE FROM rimsky_nodes WHERE instance_id = $1`, instanceID)
	return err
}

// ---- helpers ----

func scanNode(sc scannable) (storage.NodeRow, error) {
	var (
		r               storage.NodeRow
		executor        *string
		scheduleCron    *string
		currentErrClass *string
		lastHB          *time.Time
		assignedSup     *string
		frameID         *shared.UUID
	)
	if err := sc.Scan(
		&r.ID, &r.InstanceID, &r.NodeType,
		&executor, &scheduleCron, &r.State,
		&r.Dependencies,
		&currentErrClass, &r.RetryCounter, &r.ActionIndex,
		&lastHB, &assignedSup, &frameID,
		&r.CreatedAt, &r.UpdatedAt,
	); err != nil {
		return storage.NodeRow{}, err
	}
	r.Executor = derefString(executor)
	r.ScheduleCron = derefString(scheduleCron)
	r.CurrentErrorClass = derefString(currentErrClass)
	r.AssignedSupervisorID = derefString(assignedSup)
	r.LastHeartbeatAt = lastHB
	r.FrameID = frameID
	if r.Dependencies == nil {
		r.Dependencies = []shared.UUID{}
	}
	return r, nil
}

func collectNodes(rows pgx.Rows) ([]storage.NodeRow, error) {
	var out []storage.NodeRow
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
