// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// NodeTable — port of rimsky/src/storage/postgres/cell-store.ts, renamed for
// the cell→node rename (spec §11.1). The `kind` discriminator is gone; the
// store manages the `executor` nullable TEXT column. The per-node
// `schedule_cron` column retired with the 2026-05-15 plan B10 / D7 / E16
// schedule-retirement cascade; cron is owned by `sensors/sensor-cron/`.
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

// nodeCols is the canonical projection of a NodeRow. State / last_outcome
// / last_heartbeat_at / assigned_supervisor_id no longer live on the
// rimsky_nodes row (the column-drop is folded into the flattened
// baseline migration; historically migration 004 dropped them) —
// they're sourced from the in-flight rimsky_node_runs row via the LEFT
// JOIN shape below. Callers that need the projection
// use `nodeSelect` (which embeds the join) rather than the bare table.
//
// Nodes with no in-flight run row default to state='fresh' / NULL
// last_outcome / NULL heartbeat / NULL supervisor — the model is "fresh
// = no run row".
const nodeCols = `
  n.id, n.instance_id, n.node_type, n.executor,
  COALESCE(r.state, 'fresh') AS state, r.last_outcome,
  n.current_error_class, n.retry_counter, n.action_index,
  r.last_heartbeat_at, r.claimed_by AS assigned_supervisor_id,
  n.frame_id, n.tags, n.created_at, n.updated_at
`

// nodeSelect is the FROM + JOIN clause that pairs with nodeCols. The
// LATERAL subquery picks the "most-relevant" run row per node so state
// reads consistently after the rimsky_nodes column drop:
//   - if an in-flight row exists (phase in pending/active/held/parked),
//     surface its state (e.g. stale / running / parked).
//   - else surface the most-recent terminal row (phase = completed or
//     failed). This is what carries last_outcome (passed / failed /
//     fresh_changed / fresh_unchanged) into the "fresh" steady-state.
//     The state column on a completed terminal row is 'fresh'; the
//     state column on a failed terminal row is 'failed'.
//   - else state is 'fresh' (no rows; the LEFT JOIN produces NULL
//     → COALESCE('fresh')).
//
// The ORDER BY ranks in-flight above terminal; among same-rank rows
// the newest active_terminal_at / enqueued_at wins.
const nodeSelect = `FROM rimsky_nodes n
LEFT JOIN LATERAL (
    SELECT id, state, last_outcome, last_heartbeat_at, claimed_by, frame_id, phase
      FROM rimsky_node_runs
     WHERE node_id = n.id
     ORDER BY CASE WHEN phase IN ('pending','active','held','parked') THEN 0 ELSE 1 END,
              COALESCE(active_terminal_at, enqueued_at) DESC
     LIMIT 1
) r ON true`

// Create inserts a new node row with state='fresh'. Under the frame
// resolution model (docs/history/2026-04-26-frame-resolution-design.md
// §3.1), nodes transition fresh→stale only via frame-start (or via
// cascade message-pass during a running frame). Pre-frame-resolution
// the default was 'stale' because the scheduler's ready sweep was the
// initial-run trigger; under frames the trigger is the
// frame.EnqueueOrCoalesce + scheduler-tick frame engine pair.
// Create inserts a new rimsky_nodes identity row. State no longer lives
// here (post-stage-3 column drop); the implicit "no run row → fresh"
// rule means a freshly-created node defaults to state='fresh' without
// any column write. Inserts a run-row to surface that state in the
// returned NodeRow projection are not required — the LEFT JOIN in
// nodeCols produces COALESCE(r.state, 'fresh').
func (s *nodesImpl) Create(ctx context.Context, in persistence.NodeCreateInput, tx persistence.Tx) (persistence.NodeRow, error) {
	ex := s.q(tx)
	// pgx v5 maps []string transparently to TEXT[]; an empty slice (or
	// nil) lands as an empty array via the column default. Pass an empty
	// slice explicitly so the value is owned by the caller (the column
	// default would only fire if we omitted the column entirely).
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

// ListByInstancePagedFiltered narrows the page by an optional
// NodeListFilter. Empty filter is identical to ListByInstancePaged.
// Per spec
// .ok-planner/specs/2026-05-19-multi-instance-template-ergonomics-design.md
// §Item 4 (single-value tag exact-match; index-supported on postgres).
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
	// `tag = '' OR <tag> = ANY(n.tags)` — when no filter is set, the
	// first disjunct admits every row.
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

// ListReadyForDispatch returns executor-backed nodes whose in-flight
// rimsky_node_runs row is `pending`/`stale` and whose wait-set is empty.
//
// Post-stage-3 cutover: state lives on rimsky_node_runs only. The
// dispatch-ready predicate is "an in-flight run row exists in
// state='stale' AND phase='pending' AND wait-set empty for the frame".
// Post-stage-5 the wait-set keys on receiver_run_id; the gate join
// targets the in-flight run row's id directly.
//
//	@concept: wait-set
func (s *nodesImpl) ListReadyForDispatch(ctx context.Context, tx persistence.Tx) ([]persistence.NodeRow, error) {
	ex := s.q(tx)
	rows, err := ex.Query(ctx,
		`SELECT `+nodeCols+` `+nodeSelect+`
		 WHERE n.executor IS NOT NULL AND n.executor <> ''
		   AND r.state = 'stale'
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

// ListPureCascadeReady returns pure-cascade (Executor == "") nodes with
// an in-flight run row in state='stale' whose wait-set is empty in
// their current frame. The scheduler's cascade sweeper uses this list
// to flip them to 'fresh' directly without going through the dispatch
// queue.
//
//	@concept: wait-set
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

// ListRunning returns nodes with an in-flight rimsky_node_runs row in
// state='running'. Post-stage-3: state, last_heartbeat_at, and
// claimed_by all come from the run row; the remaining columns come from
// rimsky_nodes (identity + scheduling metadata).
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

func (s *nodesImpl) ListRunningBySupervisor(ctx context.Context, supervisorID string, tx persistence.Tx) ([]persistence.NodeRow, error) {
	ex := s.q(tx)
	rows, err := ex.Query(ctx,
		`SELECT `+nodeCols+` `+nodeSelect+`
		  WHERE r.state = 'running'
		    AND r.claimed_by = $1
		  ORDER BY n.updated_at ASC`, supervisorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectNodes(rows)
}

func (s *nodesImpl) ListWithStaleHeartbeat(ctx context.Context, cutoff time.Time, tx persistence.Tx) ([]persistence.NodeRow, error) {
	ex := s.q(tx)
	rows, err := ex.Query(ctx,
		`SELECT `+nodeCols+` `+nodeSelect+`
		  WHERE r.state = 'running'
		    AND r.last_heartbeat_at IS NOT NULL
		    AND r.last_heartbeat_at < $1`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectNodes(rows)
}

// CountByState aggregates current node-state counts across all nodes.
// Post-stage-3 cutover: state lives on rimsky_node_runs. Uses the same
// "most-relevant run row" lookup as nodeSelect (LATERAL picks in-flight
// over terminal-failed; nodes with no in-flight + no failed-terminal
// row count as 'fresh').
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

// enforceAndUpdate runs the node state machine and writes the resulting
// state onto the in-flight rimsky_node_runs row (post-stage-3 cutover:
// state no longer lives on rimsky_nodes).
//
// Current-state read:
//   - If an in-flight run row exists, use its state (covers
//     stale/running/failed/parked transitions).
//   - If no in-flight row exists, current state is 'fresh'.
//
// State write:
//   - On 'fresh' target: clear frame_id on rimsky_nodes. The in-flight
//     run row (if any) is closed by the terminal-handler / cascade path
//     separately; we don't touch it here.
//   - On other targets: UPDATE the in-flight row's state + last_outcome.
//     No row → no UPDATE (state machine still validated; the legal
//     fresh→stale transition arrives via MarkSourceNodeStale or
//     MarkStaleForCascade, which insert a fresh pending run row).
//
// @blessed-invariant 1: no short-circuit; the state machine alone
// decides legality (no `if from == to` skip).
func (s *nodesImpl) enforceAndUpdate(
	ctx context.Context,
	ex querier,
	id foundationshared.UUID,
	state cascade.NodeState,
	reason cascade.TransitionReason,
	lastOutcome cascade.LastOutcome,
) error {
	var (
		current       cascade.NodeState = cascade.NodeStateFresh
		runID         *foundationshared.UUID
		runFrameID    *foundationshared.UUID
		frameIDBefore *foundationshared.UUID
	)
	// Read the in-flight run row (if any) for the current state +
	// run-row frame_id. Row-lock holds for the tx so concurrent
	// updaters serialise. State writes go to this row only; non-
	// in-flight ranks (terminal-failed) do NOT take the lock — those
	// represent the node's "failed" state but cannot be transitioned
	// out of in-place (operator-reset / operator-invalidate seed a new
	// run row).
	var (
		runIDScan      *foundationshared.UUID
		stateScan      *string
		runFrameIDScan *foundationshared.UUID
	)
	err := ex.QueryRow(ctx,
		`SELECT id, state, frame_id
		   FROM rimsky_node_runs
		  WHERE node_id = $1
		    AND phase IN ('pending','active','held','parked')
		  FOR UPDATE`, id,
	).Scan(&runIDScan, &stateScan, &runFrameIDScan)
	switch {
	case err == nil:
		runID = runIDScan
		if stateScan != nil {
			current = cascade.NodeState(*stateScan)
		}
		runFrameID = runFrameIDScan
	case errors.Is(err, pgx.ErrNoRows):
		// No in-flight row. Check whether the most-recent terminal row
		// is failed (the node is currently in the failed state) so the
		// state machine can validate fresh-target reset transitions.
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
		// Otherwise current stays 'fresh'.
	default:
		return fmt.Errorf("nodes.updateState: select run row: %w", err)
	}
	// Also read the node's frame_id for the "close the closing frame's
	// progress on fresh" path. Post-cutover, the node's frame_id is the
	// node's binding to a frame; the run row's frame_id is the same in
	// the in-flight case, but we look it up explicitly for the fresh
	// case (where there is no in-flight run row).
	if err := ex.QueryRow(ctx,
		`SELECT frame_id FROM rimsky_nodes WHERE id = $1`, id,
	).Scan(&frameIDBefore); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// TS parity: silent no-op when node row gone.
			return nil
		}
		return fmt.Errorf("nodes.updateState: select node frame: %w", err)
	}
	// @blessed-invariant 1: state machine alone decides legality.
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
	var outcomeArg any
	if lastOutcome == "" {
		outcomeArg = nil
	} else {
		outcomeArg = string(lastOutcome)
	}
	// rimsky_nodes write: frame_id clear on transition to 'fresh',
	// updated_at refresh otherwise. State no longer lives here.
	if _, err := ex.Exec(ctx,
		`UPDATE rimsky_nodes
		   SET updated_at = NOW(),
		       frame_id = CASE WHEN $2 = 'fresh' THEN NULL ELSE frame_id END
		 WHERE id = $1`,
		id, string(state),
	); err != nil {
		return fmt.Errorf("nodes.updateState: rimsky_nodes update: %w", err)
	}
	// rimsky_node_runs write: state + last_outcome on the in-flight row.
	// No in-flight row means the only legal transition is into fresh
	// (handled implicitly: no state to write). A non-fresh target
	// without an in-flight row indicates a caller that should have
	// either invoked MarkSourceNodeStale / MarkStaleForCascade to seed
	// the run row, or threaded the run ID through; surface that as an
	// error so the caller can repair.
	switch {
	case runID != nil:
		if _, err := ex.Exec(ctx,
			`UPDATE rimsky_node_runs
			   SET state = $2,
			       last_outcome = COALESCE($3::text, last_outcome)
			 WHERE id = $1`,
			*runID, string(state), outcomeArg,
		); err != nil {
			return fmt.Errorf("nodes.updateState: run-row update: %w", err)
		}
	case state == cascade.NodeStateFresh:
		// Nothing to do — fresh has no in-flight row.
	case state == cascade.NodeStateStale && (current == cascade.NodeStateFailed || current == cascade.NodeStateFresh):
		// failed → stale (operator-reset / operator-invalidate path) or
		// fresh → stale (operator-invalidate). Seed a fresh pending
		// stale run row pinned to the node's existing frame_id (or
		// NULL — frame-binding is the caller's responsibility for
		// cascade walks, but operator paths leave frame_id NULL until
		// the next frame engine tick picks it up).
		//
		// rimsky_node_runs.frame_id is NOT NULL, so a NULL frame_id at
		// the node row means we cannot insert a run row yet. In that
		// case the operator-reset path must be followed by a frame
		// enqueue + advanceOneFrame call. We do best-effort here: only
		// insert when the node has a frame_id; otherwise leave the
		// in-flight set empty (the next frame-start will seed via
		// MarkSourceNodeStale).
		if frameIDBefore != nil {
			if _, err := ex.Exec(ctx,
				`INSERT INTO rimsky_node_runs
				   (id, node_id, executor_name, required_stores, enqueued_at, phase, state, last_outcome, frame_id)
				 SELECT gen_random_uuid(), n.id, n.executor, ARRAY[]::text[], NOW(), 'pending', 'stale', $3::text, $2
				   FROM rimsky_nodes n
				  WHERE n.id = $1
				    AND NOT EXISTS (
				      SELECT 1 FROM rimsky_node_runs r
				       WHERE r.node_id = $1
				         AND r.phase IN ('pending','active','held','parked')
				    )`,
				id, *frameIDBefore, outcomeArg,
			); err != nil {
				return fmt.Errorf("nodes.updateState: seed stale run-row: %w", err)
			}
		}
	default:
		// Defensive guard: any other non-fresh target without an
		// in-flight run row is a logic bug.
		return fmt.Errorf("nodes.updateState: no in-flight run row for node %s on transition to %q (reason %q)", id, state, reason.Kind)
	}
	// Refresh frame progress: any state-transition write within a frame
	// counts as progress for frame_timeout_ms's "no progress in window"
	// semantics. Use the in-flight run row's frame_id when present
	// (covers active/held/parked); fall back to the node row's frame_id
	// (covers the fresh / cascade-target window before the run row is
	// inserted).
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

// UpdateHeartbeat writes last_heartbeat_at on the in-flight rimsky_node_runs
// row. Post-stage-3 cutover: the run row is the sole heartbeat authority.
// No-op when no in-flight row exists (the caller has already passed the
// transition that removed it).
func (s *nodesImpl) UpdateHeartbeat(ctx context.Context, id foundationshared.UUID, at time.Time, supervisorID string, tx persistence.Tx) error {
	ex := s.q(tx)
	_, err := ex.Exec(ctx,
		`UPDATE rimsky_node_runs
		   SET last_heartbeat_at = $2,
		       claimed_by = COALESCE($3, claimed_by)
		 WHERE node_id = $1
		   AND phase IN ('pending','active','held','parked')`,
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

// ClearLastOutcome resets last_outcome to NULL on the in-flight run row.
// Post-stage-3 cutover: last_outcome lives on the run row only. Used by
// the operator reset path so the dashboard does not display a stale
// `failed` resolution flavor while the node transitions back through
// stale → running → fresh. No-op when no in-flight row exists.
func (s *nodesImpl) ClearLastOutcome(ctx context.Context, id foundationshared.UUID, tx persistence.Tx) error {
	ex := s.q(tx)
	if _, err := ex.Exec(ctx,
		`UPDATE rimsky_node_runs SET last_outcome = NULL
		  WHERE node_id = $1
		    AND phase IN ('pending','active','held','parked')`, id); err != nil {
		return err
	}
	// Bump rimsky_nodes.updated_at so the dashboard reorder sees the
	// reset; the row itself carries no state-machine column to clear.
	_, err := ex.Exec(ctx,
		`UPDATE rimsky_nodes SET updated_at = NOW() WHERE id = $1`, id)
	return err
}

// ClearSupervisorAssignment clears the in-flight run row's claimed_by +
// last_heartbeat_at. Post-stage-3 cutover: claimed_by lives on the run
// row. No-op when no in-flight row exists.
func (s *nodesImpl) ClearSupervisorAssignment(ctx context.Context, id foundationshared.UUID, tx persistence.Tx) error {
	ex := s.q(tx)
	_, err := ex.Exec(ctx,
		`UPDATE rimsky_node_runs
		   SET claimed_by = NULL,
		       last_heartbeat_at = NULL
		 WHERE node_id = $1
		   AND phase IN ('pending','active','held','parked')`, id)
	return err
}

func (s *nodesImpl) DeleteByInstance(ctx context.Context, instanceID foundationshared.UUID, tx persistence.Tx) error {
	ex := s.q(tx)
	_, err := ex.Exec(ctx, `DELETE FROM rimsky_nodes WHERE instance_id = $1`, instanceID)
	return err
}

// MarkStaleForCascade is the parent-commit cascade target. Post-stage-3
// cutover: the node's state lives on the in-flight rimsky_node_runs row.
//
// Eligibility (pre-cutover predicate translated):
//
//	pre: state='fresh'                          -> eligible
//	pre: state='stale' AND frame_id IS NULL     -> eligible
//	pre: any other state                         -> no-op
//
//	post: no in-flight run row                   -> eligible (insert pending stale)
//	post: in-flight pending stale, run-frame=$1  -> eligible (idempotent re-entry)
//	post: in-flight pending stale, run-frame=NULL -> not reachable (run-row frame_id NOT NULL)
//	post: any other in-flight state              -> no-op (don't touch node frame_id)
//
// When eligible: bind rimsky_nodes.frame_id = $1 and INSERT a pending
// stale run row pinned to $1. When not eligible (a running / parked /
// held in-flight row exists on a different frame), do nothing — neither
// the node row's frame_id nor the run row should be touched.
//
// Used by the supervisor's terminal-complete path so cascade children
// inherit the parent's frame_id atomically with the commit.
func (s *nodesImpl) MarkStaleForCascade(ctx context.Context, id foundationshared.UUID, frameID foundationshared.UUID, tx persistence.Tx) (bool, error) {
	ex := s.q(tx)
	// INSERT a pending stale run row only when no in-flight row exists.
	// The SELECT-WHERE-NOT-EXISTS pattern matches the pre-cutover
	// "state='fresh'" branch (no in-flight row ⇔ fresh).
	// Populates required_stores from the template node-def.
	tag, err := ex.Exec(ctx,
		`INSERT INTO rimsky_node_runs
		   (id, node_id, executor_name, required_stores, enqueued_at, phase, state, frame_id)
		 SELECT gen_random_uuid(), n.id, n.executor,
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
		        NOW(), 'pending', 'stale', $1
		   FROM rimsky_nodes n
		  WHERE n.id = $2
		    AND NOT EXISTS (
		      SELECT 1 FROM rimsky_node_runs r
		       WHERE r.node_id = $2
		         AND r.phase IN ('pending','active','held','parked')
		    )`,
		frameID, id,
	)
	if err != nil {
		return false, fmt.Errorf("nodesImpl.MarkStaleForCascade: insert run row: %w", err)
	}
	inserted := tag.RowsAffected() == 1
	// Bind node frame_id only on the eligible branches: INSERTed a new
	// run row, or an in-flight stale-pending row already pinned to $1
	// exists (idempotent re-entry). Otherwise the row is running /
	// parked / held on a different frame and we leave its node-row
	// frame_id alone.
	if inserted {
		if _, err := ex.Exec(ctx,
			`UPDATE rimsky_nodes SET frame_id = $1, updated_at = now() WHERE id = $2`,
			frameID, id,
		); err != nil {
			return false, fmt.Errorf("nodesImpl.MarkStaleForCascade: bind frame: %w", err)
		}
	}
	return inserted, nil
}

// ---- helpers ----

func scanNode(sc scannable) (persistence.NodeRow, error) {
	var (
		r               persistence.NodeRow
		executor        *string
		lastOutcome     *string
		currentErrClass *string
		lastHB          *time.Time
		assignedSup     *string
		frameID         *foundationshared.UUID
		tags            []string
	)
	if err := sc.Scan(
		&r.ID, &r.InstanceID, &r.NodeType,
		&executor, &r.State, &lastOutcome,
		&currentErrClass, &r.RetryCounter, &r.ActionIndex,
		&lastHB, &assignedSup, &frameID,
		&tags,
		&r.CreatedAt, &r.UpdatedAt,
	); err != nil {
		return persistence.NodeRow{}, err
	}
	r.Executor = derefString(executor)
	if lastOutcome != nil {
		r.LastOutcome = cascade.LastOutcome(*lastOutcome)
	}
	r.CurrentErrorClass = derefString(currentErrClass)
	r.AssignedSupervisorID = derefString(assignedSup)
	r.LastHeartbeatAt = lastHB
	r.FrameID = frameID
	// Normalize NULL-via-default to empty slice (rather than nil) so the
	// JSON encoding emits `[]` rather than `null` (per the NodeRow
	// contract: empty array means "no tags", not "unknown").
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
