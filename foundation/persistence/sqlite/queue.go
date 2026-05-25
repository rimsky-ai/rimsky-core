// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// queue.go is the SQLite implementation of persistence.Queue. Mirrors
// foundation/persistence/postgres/queue.go method-for-method with SQLite
// dialect translations per spec §6.3.
//
// @blessed-invariant 2: dispatch claim brackets the running window.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
)

// defaultCandidateLimit caps the candidate batch returned by
// SelectCandidates when the caller passes Limit==0.
const defaultCandidateLimit = 100

type queueImpl struct {
	db *sql.DB
}

func newQueue(db *sql.DB) *queueImpl { return &queueImpl{db: db} }

var _ persistence.Queue = (*queueImpl)(nil)

// q returns the right querier for tx. Same convention as tablesImpl.q.
func (q *queueImpl) q(tx persistence.Tx) querier {
	if tx == nil {
		return q.db
	}
	t, ok := tx.(*sqliteTx)
	if !ok {
		panic(fmt.Sprintf("sqlite.queueImpl.q: persistence.Tx is not a sqlite tx: %T", tx))
	}
	return t.tx
}

func (q *queueImpl) Enqueue(ctx context.Context, req persistence.DispatchRequest) error {
	return q.EnqueueInTx(ctx, req, nil)
}

// EnqueueInTx inserts a fresh dispatch row when no in-flight row exists
// for the node, otherwise no-ops. Mirrors the postgres impl post-
// stage-1 lifecycle flip: terminal rows (phase IN ('completed','failed'))
// are retained on the table so frame-end / retention / run-tree
// aggregation can read their terminal state. The uq_node_runs_in_flight
// partial unique index enforces the runtime "at most one in-flight row
// per node" invariant; the WHERE NOT EXISTS gate turns the constraint
// into a friendly no-op for the pure-cascade-sweep / retry-after-retry
// paths that may try to enqueue a row that already exists in 'pending'.
//
// Closed-scope enforcement: the INSERT's source row SELECTs from
// rimsky_run_scopes filtered by closed_at IS NULL so a new in-flight
// row cannot land in a closed RunScope. Mirrors postgres EnqueueInTx
// and AffirmNodeRunRow. On zero rows affected we re-resolve to
// distinguish "row already in-flight" (silent success), "scope closed"
// (ErrRunScopeClosed), or "scope absent" (error). SQLite's BEGIN
// IMMEDIATE write-serialisation makes the original SELECT-then-INSERT
// shape safe in practice, but folding the check into the INSERT keeps
// cross-backend symmetry and prevents future refactors from missing
// the invariant.
//
// @concept: run-scope
func (q *queueImpl) EnqueueInTx(ctx context.Context, req persistence.DispatchRequest, tx persistence.Tx) error {
	stores := req.RequiredStores
	if stores == nil {
		stores = []string{}
	}
	if req.FrameID == (shared.UUID{}) {
		return fmt.Errorf("sqlite.Enqueue: frame_id required (per blessed-invariant 19) for node %s", req.NodeID)
	}
	if req.RunScopeID == (shared.UUID{}) {
		return fmt.Errorf("sqlite.Enqueue: run_scope_id required for node %s", req.NodeID)
	}
	// Recovery-aware fields: prior_dispatch_id / prior_dispatch_disposition
	// carry the predecessor dispatch identity for retries / heartbeat-stale
	// recovery / recalculates. Both NULL for initial dispatches. Per spec
	// .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md
	// §Recovery-aware executor protocol.
	var priorDispatchID any
	if req.PriorDispatchID != nil {
		priorDispatchID = req.PriorDispatchID.String()
	}
	priorDisposition := nullableString(req.PriorDispatchDisposition)
	// Single-branch guard keyed on (node_id, run_scope_id) — unambiguous
	// per uq_node_runs_in_flight_per_run_scope. The rimsky_run_scopes
	// SELECT enforces closed_at IS NULL at INSERT time.
	res, err := q.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_node_runs (id, node_id, executor_name, required_stores, enqueued_at, phase, frame_id, run_scope_id, prior_dispatch_id, prior_dispatch_disposition)
		 SELECT ?, ?, ?, ?, ?, 'pending', ?, rs.id, ?, ?
		   FROM rimsky_run_scopes rs
		  WHERE rs.id = ?
		    AND rs.closed_at IS NULL
		    AND NOT EXISTS (
		      SELECT 1 FROM rimsky_node_runs
		       WHERE node_id = ?
		         AND run_scope_id = ?
		         AND phase IN ('pending','active','held','parked')
		    )`,
		uuid.New().String(), req.NodeID.String(),
		nullableString(req.ExecutorName), marshalStringArray(stores),
		formatTime(req.EnqueuedAt), req.FrameID.String(),
		priorDispatchID, priorDisposition,
		req.RunScopeID.String(),
		req.NodeID.String(), req.RunScopeID.String(),
	)
	if err != nil {
		return fmt.Errorf("sqlite.Enqueue: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite.Enqueue: rows affected: %w", err)
	}
	if affected > 0 {
		return nil
	}
	// Zero rows: (a) in-flight row already exists (silent success),
	// (b) scope closed (return ErrRunScopeClosed), or (c) scope absent
	// (error). Re-resolve via a separate SELECT.
	var closedAt sql.NullString
	err = q.q(tx).QueryRowContext(ctx,
		`SELECT closed_at FROM rimsky_run_scopes WHERE id = ?`,
		req.RunScopeID.String(),
	).Scan(&closedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("sqlite.Enqueue: run scope %s not found", req.RunScopeID)
	}
	if err != nil {
		return fmt.Errorf("sqlite.Enqueue: lookup run scope: %w", err)
	}
	if closedAt.Valid {
		return persistence.ErrRunScopeClosed
	}
	// Scope is open: zero rows means an in-flight row already exists;
	// silent success per the existing no-op contract.
	return nil
}

// SelectCandidates returns up to req.Limit dispatch rows. SQLite has no
// FOR UPDATE SKIP LOCKED; the surrounding BEGIN IMMEDIATE writer-slot
// hold serialises any concurrent runner so there's no contention to skip.
func (q *queueImpl) SelectCandidates(
	ctx context.Context, tx persistence.Tx, req persistence.SelectCandidatesRequest,
) ([]persistence.Candidate, error) {
	if tx == nil {
		return nil, errors.New("sqlite.SelectCandidates: tx required")
	}
	if _, err := unwrapTx(tx); err != nil {
		return nil, fmt.Errorf("sqlite.SelectCandidates: %w", err)
	}
	limit := req.Limit
	if limit <= 0 {
		limit = defaultCandidateLimit
	}
	acceptedStores := req.AcceptedStores
	if acceptedStores == nil {
		acceptedStores = []string{}
	}
	acceptedExecutors := req.AcceptedExecutors
	if acceptedExecutors == nil {
		acceptedExecutors = []string{}
	}

	// SQLite: filter required_stores ⊆ acceptedStores in app code by
	// scanning then post-filtering. We push the executor and now filters
	// into SQL where straightforward, but executor `IN (...)` and
	// required_stores subset are easier in Go.
	// SELECT predicates: claimed_by IS NULL is the legacy unclaimed-row
	// gate; phase='pending' filters out parked rows (which also have
	// claimed_by=NULL but must transition through wakeParkedNode rather
	// than being directly claimed). Per the 2026-05-08 platform-extensions
	// plan E2/E3.
	// Join rimsky_instances via rimsky_nodes (rimsky_node_runs has no
	// instance_id of its own) and filter out paused instances. Per
	// concept:breakpoint §5.2 soft-pause semantics.
	rows, err := q.q(tx).QueryContext(ctx,
		`SELECT d.id, d.node_id, n.node_type, d.executor_name, d.required_stores, d.enqueued_at, d.frame_id,
		        d.prior_dispatch_id, d.prior_dispatch_disposition
		   FROM rimsky_node_runs d
		   JOIN rimsky_nodes n ON n.id = d.node_id
		   JOIN rimsky_instances i ON i.id = n.instance_id
		  WHERE d.claimed_by IS NULL
		    AND d.phase = 'pending'
		    AND i.paused = 0
		    AND d.enqueued_at <= ?
		    AND NOT EXISTS (
		      SELECT 1 FROM rimsky_wait_set w
		      WHERE w.frame_id = d.frame_id AND w.receiver_run_id = d.id
		        AND w.drained_at IS NULL
		    )
		  ORDER BY d.enqueued_at`,
		nowUTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite.SelectCandidates: %w", err)
	}
	defer rows.Close()

	// Post-stage-3 cutover: native-only rows (executor == "") need a
	// non-empty required_stores to be dispatch-eligible; pure-cascade
	// rows (no executor, no stores) are state-only and excluded.
	executorAccepted := func(executor string, required []string) bool {
		if executor == "" {
			return len(required) > 0
		}
		for _, a := range acceptedExecutors {
			if a == executor {
				return true
			}
		}
		return false
	}
	storeAccepted := func(required []string) bool {
		// required ⊆ acceptedStores
		for _, r := range required {
			found := false
			for _, a := range acceptedStores {
				if a == r {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}

	var out []persistence.Candidate
	for rows.Next() {
		var (
			c                   persistence.Candidate
			dispatchIDStr       string
			nodeIDStr           string
			nodeType            string
			executorName        sql.NullString
			requiredStoresStr   string
			enqueuedAtStr       string
			frameIDStr          string
			priorDispatchIDStr  sql.NullString
			priorDispositionStr sql.NullString
		)
		if err := rows.Scan(&dispatchIDStr, &nodeIDStr, &nodeType, &executorName,
			&requiredStoresStr, &enqueuedAtStr, &frameIDStr,
			&priorDispatchIDStr, &priorDispositionStr); err != nil {
			return nil, fmt.Errorf("sqlite.SelectCandidates: scan: %w", err)
		}
		c.NodeType = nodeType
		if executorName.Valid {
			c.ExecutorName = executorName.String
		}
		if priorDispatchIDStr.Valid {
			pid, perr := uuid.Parse(priorDispatchIDStr.String)
			if perr != nil {
				return nil, fmt.Errorf("sqlite.SelectCandidates: parse prior_dispatch_id: %w", perr)
			}
			c.PriorDispatchID = &pid
		}
		if priorDispositionStr.Valid {
			c.PriorDispatchDisposition = priorDispositionStr.String
		}
		stores, err := unmarshalStringArray(requiredStoresStr)
		if err != nil {
			return nil, err
		}
		c.RequiredStores = stores
		if !executorAccepted(c.ExecutorName, c.RequiredStores) || !storeAccepted(c.RequiredStores) {
			continue
		}
		if c.DispatchID, err = uuid.Parse(dispatchIDStr); err != nil {
			return nil, err
		}
		if c.NodeID, err = uuid.Parse(nodeIDStr); err != nil {
			return nil, err
		}
		if c.FrameID, err = uuid.Parse(frameIDStr); err != nil {
			return nil, err
		}
		if c.EnqueuedAt, err = parseTime(enqueuedAtStr); err != nil {
			return nil, err
		}
		out = append(out, c)
		if len(out) >= limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite.SelectCandidates: rows: %w", err)
	}
	return out, nil
}

// ClaimDispatchRow — @blessed-invariant 2.
//
// The phase='pending' guard prevents accidental claims of parked rows;
// see the matching postgres impl for rationale.
func (q *queueImpl) ClaimDispatchRow(
	ctx context.Context, tx persistence.Tx, dispatchID shared.UUID, supervisorID string,
) (bool, error) {
	if tx == nil {
		return false, errors.New("sqlite.ClaimDispatchRow: tx required")
	}
	now := nowUTC()
	res, err := q.q(tx).ExecContext(ctx,
		`UPDATE rimsky_node_runs
		    SET claimed_by = ?, claimed_at = ?, last_heartbeat_at = ?, phase = 'active'
		  WHERE id = ? AND claimed_by IS NULL AND phase = 'pending'`,
		supervisorID, now, now, dispatchID.String(),
	)
	if err != nil {
		return false, fmt.Errorf("sqlite.ClaimDispatchRow: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// Complete retires the in-flight run row identified by dispatchID. Post-
// stage-1 lifecycle flip: flips phase to a terminal value rather than
// deleting the row so frame-end / retention / run-tree aggregation can
// read the terminal `state` / `settling_signal_type` after the active
// phase closes. See the postgres impl for the full rationale.
func (q *queueImpl) Complete(ctx context.Context, dispatchID shared.UUID, expectedClaimedBy string) error {
	now := nowUTC()
	if expectedClaimedBy != "" {
		_, err := q.db.ExecContext(ctx,
			`UPDATE rimsky_node_runs
			    SET phase = CASE WHEN state = 'failed' THEN 'failed' ELSE 'completed' END,
			        claimed_by = NULL,
			        last_heartbeat_at = NULL,
			        active_terminal_at = ?
			  WHERE id = ?
			    AND claimed_by = ?
			    AND phase IN ('pending','active','held','parked')`,
			now, dispatchID.String(), expectedClaimedBy,
		)
		return err
	}
	_, err := q.db.ExecContext(ctx,
		`UPDATE rimsky_node_runs
		    SET phase = CASE WHEN state = 'failed' THEN 'failed' ELSE 'completed' END,
		        claimed_by = NULL,
		        last_heartbeat_at = NULL,
		        active_terminal_at = ?
		  WHERE id = ?
		    AND phase IN ('pending','active','held','parked')`,
		now, dispatchID.String(),
	)
	return err
}

func (q *queueImpl) RemoveForNode(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID, expectedClaimedBy string) error {
	return q.RemoveForNodeInTx(ctx, nodeID, runScopeID, expectedClaimedBy, nil)
}

// RemoveForNodeInTx retires the in-flight run row for a (node, run scope)
// by flipping phase to a terminal value.
//
// @blessed-invariant 4: claimant-guarded release.
// @concept: run-scope
func (q *queueImpl) RemoveForNodeInTx(ctx context.Context, nodeID shared.UUID, runScopeID shared.UUID, expectedClaimedBy string, tx persistence.Tx) error {
	now := nowUTC()
	if expectedClaimedBy != "" {
		_, err := q.q(tx).ExecContext(ctx,
			`UPDATE rimsky_node_runs
			    SET phase = CASE WHEN state = 'failed' THEN 'failed' ELSE 'completed' END,
			        claimed_by = NULL,
			        last_heartbeat_at = NULL,
			        active_terminal_at = ?
			  WHERE node_id = ?
			    AND run_scope_id = ?
			    AND claimed_by = ?
			    AND phase IN ('pending','active','held','parked')`,
			now, nodeID.String(), runScopeID.String(), expectedClaimedBy,
		)
		return err
	}
	_, err := q.q(tx).ExecContext(ctx,
		`UPDATE rimsky_node_runs
		    SET phase = CASE WHEN state = 'failed' THEN 'failed' ELSE 'completed' END,
		        claimed_by = NULL,
		        last_heartbeat_at = NULL,
		        active_terminal_at = ?
		  WHERE node_id = ?
		    AND run_scope_id = ?
		    AND phase IN ('pending','active','held','parked')`,
		now, nodeID.String(), runScopeID.String(),
	)
	return err
}

// ListOrphanedClaims returns dispatch rows whose last_heartbeat_at is
// older than cutoff. @blessed-invariant 6 (5× heartbeat cutoff).
func (q *queueImpl) ListOrphanedClaims(ctx context.Context, cutoff time.Time) ([]persistence.DispatchRow, error) {
	rows, err := q.db.QueryContext(ctx,
		`SELECT id, node_id, executor_name, required_stores, enqueued_at,
		        claimed_by, claimed_at, last_heartbeat_at, frame_id
		   FROM rimsky_node_runs
		  WHERE claimed_by IS NOT NULL
		    AND last_heartbeat_at < ?`,
		formatTime(cutoff),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []persistence.DispatchRow
	for rows.Next() {
		var r persistence.DispatchRow
		var (
			idStr             string
			nodeIDStr         string
			executorName      sql.NullString
			requiredStoresStr string
			enqueuedAtStr     string
			claimedBy         sql.NullString
			claimedAtStr      sql.NullString
			lastHeartbeatStr  sql.NullString
			frameIDStr        string
		)
		if err := rows.Scan(
			&idStr, &nodeIDStr, &executorName, &requiredStoresStr,
			&enqueuedAtStr, &claimedBy, &claimedAtStr, &lastHeartbeatStr, &frameIDStr,
		); err != nil {
			return nil, err
		}
		var err error
		if r.ID, err = uuid.Parse(idStr); err != nil {
			return nil, err
		}
		if r.NodeID, err = uuid.Parse(nodeIDStr); err != nil {
			return nil, err
		}
		if executorName.Valid {
			v := executorName.String
			r.ExecutorName = &v
		}
		stores, err := unmarshalStringArray(requiredStoresStr)
		if err != nil {
			return nil, err
		}
		r.RequiredStores = stores
		if r.EnqueuedAt, err = parseTime(enqueuedAtStr); err != nil {
			return nil, err
		}
		if claimedBy.Valid {
			v := claimedBy.String
			r.ClaimedBy = &v
		}
		if claimedAtStr.Valid {
			t, err := parseTime(claimedAtStr.String)
			if err != nil {
				return nil, err
			}
			r.ClaimedAt = &t
		}
		if lastHeartbeatStr.Valid {
			t, err := parseTime(lastHeartbeatStr.String)
			if err != nil {
				return nil, err
			}
			r.LastHeartbeatAt = &t
		}
		if r.FrameID, err = uuid.Parse(frameIDStr); err != nil {
			return nil, err
		}
		if r.RequiredStores == nil {
			r.RequiredStores = []string{}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ReleaseClaim — @blessed-invariant 4. Reverts phase to 'pending'
// alongside nulling the claim fields so the orphan reaper's repointed
// row becomes claim-eligible again on the next scheduler tick.
func (q *queueImpl) ReleaseClaim(ctx context.Context, dispatchID shared.UUID, expectedClaimedBy string) error {
	if expectedClaimedBy != "" {
		_, err := q.db.ExecContext(ctx,
			`UPDATE rimsky_node_runs
			    SET claimed_by = NULL, claimed_at = NULL, last_heartbeat_at = NULL, phase = 'pending'
			  WHERE id = ? AND claimed_by = ?`,
			dispatchID.String(), expectedClaimedBy,
		)
		return err
	}
	_, err := q.db.ExecContext(ctx,
		`UPDATE rimsky_node_runs
		    SET claimed_by = NULL, claimed_at = NULL, last_heartbeat_at = NULL, phase = 'pending'
		  WHERE id = ?`,
		dispatchID.String(),
	)
	return err
}

func (q *queueImpl) GetDispatchNode(ctx context.Context, dispatchID shared.UUID) (shared.UUID, persistence.ClaimOwnership, error) {
	var (
		nodeIDStr string
		claimedBy sql.NullString
	)
	err := q.db.QueryRowContext(ctx,
		`SELECT node_id, claimed_by FROM rimsky_node_runs WHERE id = ?`,
		dispatchID.String(),
	).Scan(&nodeIDStr, &claimedBy)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return shared.UUID{}, persistence.ClaimOwnership{Kind: "not_found"}, nil
		}
		return shared.UUID{}, persistence.ClaimOwnership{}, err
	}
	nodeID, perr := uuid.Parse(nodeIDStr)
	if perr != nil {
		return shared.UUID{}, persistence.ClaimOwnership{}, perr
	}
	if !claimedBy.Valid {
		return nodeID, persistence.ClaimOwnership{Kind: "unclaimed"}, nil
	}
	return nodeID, persistence.ClaimOwnership{Kind: "claimed_by", SupervisorID: claimedBy.String}, nil
}

// GetClaimedBy — @blessed-invariant 5: verify-before-run.
func (q *queueImpl) GetClaimedBy(ctx context.Context, dispatchID shared.UUID) (persistence.ClaimOwnership, error) {
	var claimedBy sql.NullString
	err := q.db.QueryRowContext(ctx,
		`SELECT claimed_by FROM rimsky_node_runs WHERE id = ?`,
		dispatchID.String(),
	).Scan(&claimedBy)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return persistence.ClaimOwnership{Kind: "not_found"}, nil
		}
		return persistence.ClaimOwnership{}, err
	}
	if !claimedBy.Valid {
		return persistence.ClaimOwnership{Kind: "unclaimed"}, nil
	}
	return persistence.ClaimOwnership{Kind: "claimed_by", SupervisorID: claimedBy.String}, nil
}

func (q *queueImpl) RefreshHeartbeat(ctx context.Context, supervisorID string) error {
	_, err := q.db.ExecContext(ctx,
		`UPDATE rimsky_node_runs SET last_heartbeat_at = ? WHERE claimed_by = ?`,
		nowUTC(), supervisorID,
	)
	if err != nil {
		return fmt.Errorf("sqlite.RefreshHeartbeat: %w", err)
	}
	return nil
}

// ListLive returns currently-live dispatch rows for the observability
// browse endpoint. Cursor pagination over (enqueued_at DESC, id DESC).
func (q *queueImpl) ListLive(ctx context.Context, filter persistence.DispatchListFilter, pag persistence.ListPagination) (persistence.PaginatedListResult[persistence.DispatchRow], error) {
	limit := pag.Limit
	if limit <= 0 {
		limit = 50
	}
	stateClause, executor, instanceID := buildLiveDispatchFilters(filter)
	args := []any{}
	args = append(args, executor, executor)
	args = append(args, instanceID, instanceID)
	cursorClause := ""
	if pag.Cursor != "" {
		oc, id, err := decodeDispatchCursor(pag.Cursor)
		if err != nil {
			return persistence.PaginatedListResult[persistence.DispatchRow]{}, fmt.Errorf("sqlite.ListLive: bad cursor: %w", err)
		}
		cursorClause = " AND (d.enqueued_at, d.id) < (?, ?)"
		args = append(args, formatTime(oc), id.String())
	}
	args = append(args, limit)
	// Post-stage-1 lifecycle flip: terminal rows survive past active
	// terminal; the "live" observability surface filters to in-flight
	// phases so the listing keeps its prior shape.
	q1 := `SELECT d.id, d.node_id, d.executor_name, d.required_stores, d.enqueued_at,
	        d.claimed_by, d.claimed_at, d.last_heartbeat_at, d.frame_id
	   FROM rimsky_node_runs d
	   LEFT JOIN rimsky_nodes n ON n.id = d.node_id
	  WHERE d.phase IN ('pending','active','held','parked')` +
		stateClause +
		` AND (? IS NULL OR d.executor_name = ?)
	    AND (? IS NULL OR n.instance_id = ?)` +
		cursorClause +
		` ORDER BY d.enqueued_at DESC, d.id DESC
	  LIMIT ?`
	rows, err := q.db.QueryContext(ctx, q1, args...)
	if err != nil {
		return persistence.PaginatedListResult[persistence.DispatchRow]{}, err
	}
	defer rows.Close()
	var out []persistence.DispatchRow
	for rows.Next() {
		row, err := scanDispatchRow(rows)
		if err != nil {
			return persistence.PaginatedListResult[persistence.DispatchRow]{}, err
		}
		out = append(out, row)
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

// CountLive counts currently-live dispatch rows matching filter.
func (q *queueImpl) CountLive(ctx context.Context, filter persistence.DispatchListFilter) (int, error) {
	stateClause, executor, instanceID := buildLiveDispatchFilters(filter)
	q1 := `SELECT COUNT(*)
	   FROM rimsky_node_runs d
	   LEFT JOIN rimsky_nodes n ON n.id = d.node_id
	  WHERE d.phase IN ('pending','active','held','parked')` +
		stateClause +
		` AND (? IS NULL OR d.executor_name = ?)
	    AND (? IS NULL OR n.instance_id = ?)`
	var n int
	err := q.db.QueryRowContext(ctx, q1, executor, executor, instanceID, instanceID).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// CountParkedByReason mirrors the postgres impl: groups currently-parked
// node-run rows by parked_reason for the metrics gauge. Empty /
// NULL reason buckets under "".
func (q *queueImpl) CountParkedByReason(ctx context.Context) (map[string]int, error) {
	rows, err := q.db.QueryContext(ctx,
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

// GetByID returns the live dispatch row for id, or (nil, nil) when no
// such row exists (or the row has reached terminal phase). Mirrors the
// postgres impl for the observability dispatch-detail handler.
func (q *queueImpl) GetByID(ctx context.Context, id shared.UUID) (*persistence.DispatchRow, error) {
	row := q.db.QueryRowContext(ctx,
		`SELECT d.id, d.node_id, d.executor_name, d.required_stores, d.enqueued_at,
		        d.claimed_by, d.claimed_at, d.last_heartbeat_at, d.frame_id
		   FROM rimsky_node_runs d
		  WHERE d.id = ?
		    AND d.phase IN ('pending','active','held','parked')`, id.String(),
	)
	var (
		idStr             string
		nodeIDStr         string
		executorName      sql.NullString
		requiredStoresStr string
		enqueuedAtStr     string
		claimedBy         sql.NullString
		claimedAtStr      sql.NullString
		lastHeartbeatStr  sql.NullString
		frameIDStr        string
	)
	if err := row.Scan(
		&idStr, &nodeIDStr, &executorName, &requiredStoresStr,
		&enqueuedAtStr, &claimedBy, &claimedAtStr, &lastHeartbeatStr, &frameIDStr,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	var r persistence.DispatchRow
	var err error
	if r.ID, err = uuid.Parse(idStr); err != nil {
		return nil, err
	}
	if r.NodeID, err = uuid.Parse(nodeIDStr); err != nil {
		return nil, err
	}
	if executorName.Valid {
		v := executorName.String
		r.ExecutorName = &v
	}
	stores, err := unmarshalStringArray(requiredStoresStr)
	if err != nil {
		return nil, err
	}
	r.RequiredStores = stores
	if r.EnqueuedAt, err = parseTime(enqueuedAtStr); err != nil {
		return nil, err
	}
	if claimedBy.Valid {
		v := claimedBy.String
		r.ClaimedBy = &v
	}
	if claimedAtStr.Valid {
		t, err := parseTime(claimedAtStr.String)
		if err != nil {
			return nil, err
		}
		r.ClaimedAt = &t
	}
	if lastHeartbeatStr.Valid {
		t, err := parseTime(lastHeartbeatStr.String)
		if err != nil {
			return nil, err
		}
		r.LastHeartbeatAt = &t
	}
	if r.FrameID, err = uuid.Parse(frameIDStr); err != nil {
		return nil, err
	}
	if r.RequiredStores == nil {
		r.RequiredStores = []string{}
	}
	return &r, nil
}

// GetInFlightRunForNode resolves the in-flight rimsky_node_runs.id for
// the (node, run scope) pair. Unambiguous per uq_node_runs_in_flight_per_run_scope.
//
// @concept: run-scope
func (q *queueImpl) GetInFlightRunForNode(ctx context.Context, tx persistence.Tx, nodeID, runScopeID shared.UUID) (shared.UUID, bool, error) {
	row := q.q(tx).QueryRowContext(ctx,
		`SELECT id FROM rimsky_node_runs
		  WHERE node_id = ? AND run_scope_id = ?
		    AND phase IN ('pending','active','held','parked')
		  LIMIT 1`,
		nodeID.String(), runScopeID.String())
	var idStr string
	if err := row.Scan(&idStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return shared.UUID{}, false, nil
		}
		return shared.UUID{}, false, fmt.Errorf("sqlite.GetInFlightRunForNode: %w", err)
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return shared.UUID{}, false, fmt.Errorf("sqlite.GetInFlightRunForNode: parse %q: %w", idStr, err)
	}
	return id, true, nil
}

func buildLiveDispatchFilters(filter persistence.DispatchListFilter) (stateClause string, executor any, instanceID any) {
	switch filter.State {
	case "pending":
		stateClause = " AND d.claimed_by IS NULL"
	case "claimed":
		stateClause = " AND d.claimed_by IS NOT NULL"
	}
	if filter.ExecutorName != "" {
		executor = filter.ExecutorName
	}
	if filter.InstanceID != nil {
		instanceID = filter.InstanceID.String()
	}
	return stateClause, executor, instanceID
}

func scanDispatchRow(rows *sql.Rows) (persistence.DispatchRow, error) {
	var (
		idStr             string
		nodeIDStr         string
		executorName      sql.NullString
		requiredStoresStr string
		enqueuedAtStr     string
		claimedBy         sql.NullString
		claimedAtStr      sql.NullString
		lastHeartbeatStr  sql.NullString
		frameIDStr        string
		r                 persistence.DispatchRow
	)
	if err := rows.Scan(
		&idStr, &nodeIDStr, &executorName, &requiredStoresStr,
		&enqueuedAtStr, &claimedBy, &claimedAtStr, &lastHeartbeatStr, &frameIDStr,
	); err != nil {
		return persistence.DispatchRow{}, err
	}
	var err error
	if r.ID, err = uuid.Parse(idStr); err != nil {
		return persistence.DispatchRow{}, err
	}
	if r.NodeID, err = uuid.Parse(nodeIDStr); err != nil {
		return persistence.DispatchRow{}, err
	}
	if executorName.Valid {
		v := executorName.String
		r.ExecutorName = &v
	}
	stores, err := unmarshalStringArray(requiredStoresStr)
	if err != nil {
		return persistence.DispatchRow{}, err
	}
	r.RequiredStores = stores
	if r.EnqueuedAt, err = parseTime(enqueuedAtStr); err != nil {
		return persistence.DispatchRow{}, err
	}
	if claimedBy.Valid {
		v := claimedBy.String
		r.ClaimedBy = &v
	}
	if claimedAtStr.Valid {
		t, err := parseTime(claimedAtStr.String)
		if err != nil {
			return persistence.DispatchRow{}, err
		}
		r.ClaimedAt = &t
	}
	if lastHeartbeatStr.Valid {
		t, err := parseTime(lastHeartbeatStr.String)
		if err != nil {
			return persistence.DispatchRow{}, err
		}
		r.LastHeartbeatAt = &t
	}
	if r.FrameID, err = uuid.Parse(frameIDStr); err != nil {
		return persistence.DispatchRow{}, err
	}
	if r.RequiredStores == nil {
		r.RequiredStores = []string{}
	}
	return r, nil
}

func encodeDispatchCursor(enqueued time.Time, id shared.UUID) string {
	c := struct {
		E time.Time `json:"e"`
		I string    `json:"i"`
	}{E: enqueued, I: id.String()}
	b, _ := json.Marshal(c)
	return base64.StdEncoding.EncodeToString(b)
}

func decodeDispatchCursor(s string) (time.Time, shared.UUID, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, shared.UUID{}, err
	}
	var c struct {
		E time.Time `json:"e"`
		I string    `json:"i"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return time.Time{}, shared.UUID{}, err
	}
	id, err := uuid.Parse(c.I)
	if err != nil {
		return time.Time{}, shared.UUID{}, err
	}
	return c.E, id, nil
}
