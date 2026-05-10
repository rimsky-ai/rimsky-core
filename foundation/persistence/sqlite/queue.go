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
	"github.com/fallguy/rimsky/modeling/shared"
)

// defaultCandidateLimit caps the candidate batch returned by
// SelectCandidates when the caller passes Limit==0.
const defaultCandidateLimit = 100

type queueImpl struct {
	db *sql.DB
}

func newQueue(db *sql.DB) *queueImpl { return &queueImpl{db: db} }

var _ persistence.Queue = (*queueImpl)(nil)

// q returns the right querier for tx. Same convention as storeImpl.q.
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

func (q *queueImpl) EnqueueInTx(ctx context.Context, req persistence.DispatchRequest, tx persistence.Tx) error {
	stores := req.RequiredStores
	if stores == nil {
		stores = []string{}
	}
	if req.FrameID == (shared.UUID{}) {
		return fmt.Errorf("sqlite.Enqueue: frame_id required (per blessed-invariant 19) for node %s", req.NodeID)
	}
	now := nowUTC()
	_, err := q.q(tx).ExecContext(ctx,
		`INSERT INTO rimsky_worker_request (id, node_id, executor_name, required_stores, enqueued_at, phase, frame_id)
		 VALUES (?, ?, ?, ?, ?, 'pending', ?)
		 ON CONFLICT(node_id) DO UPDATE
		   SET enqueued_at = excluded.enqueued_at,
		       executor_name = excluded.executor_name,
		       required_stores = excluded.required_stores,
		       frame_id = excluded.frame_id
		   WHERE rimsky_worker_request.claimed_by IS NULL
		     AND rimsky_worker_request.phase = 'pending'
		     AND rimsky_worker_request.enqueued_at <= ?`,
		uuid.New().String(), req.NodeID.String(),
		nullableString(req.ExecutorName), marshalStringArray(stores),
		formatTime(req.EnqueuedAt), req.FrameID.String(),
		now,
	)
	return err
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
	rows, err := q.q(tx).QueryContext(ctx,
		`SELECT d.id, d.node_id, n.node_type, d.executor_name, d.required_stores, d.enqueued_at, d.frame_id
		   FROM rimsky_worker_request d
		   JOIN rimsky_nodes n ON n.id = d.node_id
		  WHERE d.claimed_by IS NULL
		    AND d.phase = 'pending'
		    AND d.enqueued_at <= ?
		  ORDER BY d.enqueued_at`,
		nowUTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite.SelectCandidates: %w", err)
	}
	defer rows.Close()

	executorAccepted := func(executor string) bool {
		if executor == "" {
			return true // native node
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
			c                 persistence.Candidate
			dispatchIDStr     string
			nodeIDStr         string
			nodeType          string
			executorName      sql.NullString
			requiredStoresStr string
			enqueuedAtStr     string
			frameIDStr        string
		)
		if err := rows.Scan(&dispatchIDStr, &nodeIDStr, &nodeType, &executorName,
			&requiredStoresStr, &enqueuedAtStr, &frameIDStr); err != nil {
			return nil, fmt.Errorf("sqlite.SelectCandidates: scan: %w", err)
		}
		c.NodeType = nodeType
		if executorName.Valid {
			c.ExecutorName = executorName.String
		}
		stores, err := unmarshalStringArray(requiredStoresStr)
		if err != nil {
			return nil, err
		}
		c.RequiredStores = stores
		if !executorAccepted(c.ExecutorName) || !storeAccepted(c.RequiredStores) {
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
		`UPDATE rimsky_worker_request
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

func (q *queueImpl) Complete(ctx context.Context, dispatchID shared.UUID, expectedClaimedBy string) error {
	if expectedClaimedBy != "" {
		_, err := q.db.ExecContext(ctx,
			`DELETE FROM rimsky_worker_request WHERE id = ? AND claimed_by = ?`,
			dispatchID.String(), expectedClaimedBy,
		)
		return err
	}
	_, err := q.db.ExecContext(ctx,
		`DELETE FROM rimsky_worker_request WHERE id = ?`, dispatchID.String(),
	)
	return err
}

func (q *queueImpl) RemoveForNode(ctx context.Context, nodeID shared.UUID, expectedClaimedBy string) error {
	return q.RemoveForNodeInTx(ctx, nodeID, expectedClaimedBy, nil)
}

func (q *queueImpl) RemoveForNodeInTx(ctx context.Context, nodeID shared.UUID, expectedClaimedBy string, tx persistence.Tx) error {
	if expectedClaimedBy != "" {
		_, err := q.q(tx).ExecContext(ctx,
			`DELETE FROM rimsky_worker_request WHERE node_id = ? AND claimed_by = ?`,
			nodeID.String(), expectedClaimedBy,
		)
		return err
	}
	_, err := q.q(tx).ExecContext(ctx,
		`DELETE FROM rimsky_worker_request WHERE node_id = ?`, nodeID.String(),
	)
	return err
}

// ListOrphanedClaims returns dispatch rows whose last_heartbeat_at is
// older than cutoff. @blessed-invariant 6 (5× heartbeat cutoff).
func (q *queueImpl) ListOrphanedClaims(ctx context.Context, cutoff time.Time) ([]shared.DispatchRow, error) {
	rows, err := q.db.QueryContext(ctx,
		`SELECT id, node_id, executor_name, required_stores, enqueued_at,
		        claimed_by, claimed_at, last_heartbeat_at, frame_id
		   FROM rimsky_worker_request
		  WHERE claimed_by IS NOT NULL
		    AND last_heartbeat_at < ?`,
		formatTime(cutoff),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []shared.DispatchRow
	for rows.Next() {
		var r shared.DispatchRow
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
			`UPDATE rimsky_worker_request
			    SET claimed_by = NULL, claimed_at = NULL, last_heartbeat_at = NULL, phase = 'pending'
			  WHERE id = ? AND claimed_by = ?`,
			dispatchID.String(), expectedClaimedBy,
		)
		return err
	}
	_, err := q.db.ExecContext(ctx,
		`UPDATE rimsky_worker_request
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
		`SELECT node_id, claimed_by FROM rimsky_worker_request WHERE id = ?`,
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
		`SELECT claimed_by FROM rimsky_worker_request WHERE id = ?`,
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
		`UPDATE rimsky_worker_request SET last_heartbeat_at = ? WHERE claimed_by = ?`,
		nowUTC(), supervisorID,
	)
	if err != nil {
		return fmt.Errorf("sqlite.RefreshHeartbeat: %w", err)
	}
	return nil
}

// ListLive returns currently-live dispatch rows for the observability
// browse endpoint. Cursor pagination over (enqueued_at DESC, id DESC).
func (q *queueImpl) ListLive(ctx context.Context, filter persistence.DispatchListFilter, pag persistence.ListPagination) (persistence.PaginatedListResult[shared.DispatchRow], error) {
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
			return persistence.PaginatedListResult[shared.DispatchRow]{}, fmt.Errorf("sqlite.ListLive: bad cursor: %w", err)
		}
		cursorClause = " AND (d.enqueued_at, d.id) < (?, ?)"
		args = append(args, formatTime(oc), id.String())
	}
	args = append(args, limit)
	q1 := `SELECT d.id, d.node_id, d.executor_name, d.required_stores, d.enqueued_at,
	        d.claimed_by, d.claimed_at, d.last_heartbeat_at, d.frame_id
	   FROM rimsky_worker_request d
	   LEFT JOIN rimsky_nodes n ON n.id = d.node_id
	  WHERE 1=1` +
		stateClause +
		` AND (? IS NULL OR d.executor_name = ?)
	    AND (? IS NULL OR n.instance_id = ?)` +
		cursorClause +
		` ORDER BY d.enqueued_at DESC, d.id DESC
	  LIMIT ?`
	rows, err := q.db.QueryContext(ctx, q1, args...)
	if err != nil {
		return persistence.PaginatedListResult[shared.DispatchRow]{}, err
	}
	defer rows.Close()
	var out []shared.DispatchRow
	for rows.Next() {
		row, err := scanDispatchRow(rows)
		if err != nil {
			return persistence.PaginatedListResult[shared.DispatchRow]{}, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return persistence.PaginatedListResult[shared.DispatchRow]{}, err
	}
	var nextCursor string
	if len(out) == limit && len(out) > 0 {
		last := out[len(out)-1]
		nextCursor = encodeDispatchCursor(last.EnqueuedAt, last.ID)
	}
	return persistence.PaginatedListResult[shared.DispatchRow]{Rows: out, NextCursor: nextCursor}, nil
}

// CountLive counts currently-live dispatch rows matching filter.
func (q *queueImpl) CountLive(ctx context.Context, filter persistence.DispatchListFilter) (int, error) {
	stateClause, executor, instanceID := buildLiveDispatchFilters(filter)
	q1 := `SELECT COUNT(*)
	   FROM rimsky_worker_request d
	   LEFT JOIN rimsky_nodes n ON n.id = d.node_id
	  WHERE 1=1` +
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
// worker_request rows by parked_reason for the metrics gauge. Empty /
// NULL reason buckets under "".
func (q *queueImpl) CountParkedByReason(ctx context.Context) (map[string]int, error) {
	rows, err := q.db.QueryContext(ctx,
		`SELECT COALESCE(parked_reason, ''), COUNT(*)
		   FROM rimsky_worker_request
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
// such row exists. Mirrors the postgres impl for the observability
// dispatch-detail handler.
func (q *queueImpl) GetByID(ctx context.Context, id shared.UUID) (*shared.DispatchRow, error) {
	row := q.db.QueryRowContext(ctx,
		`SELECT d.id, d.node_id, d.executor_name, d.required_stores, d.enqueued_at,
		        d.claimed_by, d.claimed_at, d.last_heartbeat_at, d.frame_id
		   FROM rimsky_worker_request d
		  WHERE d.id = ?`, id.String(),
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
	var r shared.DispatchRow
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

func scanDispatchRow(rows *sql.Rows) (shared.DispatchRow, error) {
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
		r                 shared.DispatchRow
	)
	if err := rows.Scan(
		&idStr, &nodeIDStr, &executorName, &requiredStoresStr,
		&enqueuedAtStr, &claimedBy, &claimedAtStr, &lastHeartbeatStr, &frameIDStr,
	); err != nil {
		return shared.DispatchRow{}, err
	}
	var err error
	if r.ID, err = uuid.Parse(idStr); err != nil {
		return shared.DispatchRow{}, err
	}
	if r.NodeID, err = uuid.Parse(nodeIDStr); err != nil {
		return shared.DispatchRow{}, err
	}
	if executorName.Valid {
		v := executorName.String
		r.ExecutorName = &v
	}
	stores, err := unmarshalStringArray(requiredStoresStr)
	if err != nil {
		return shared.DispatchRow{}, err
	}
	r.RequiredStores = stores
	if r.EnqueuedAt, err = parseTime(enqueuedAtStr); err != nil {
		return shared.DispatchRow{}, err
	}
	if claimedBy.Valid {
		v := claimedBy.String
		r.ClaimedBy = &v
	}
	if claimedAtStr.Valid {
		t, err := parseTime(claimedAtStr.String)
		if err != nil {
			return shared.DispatchRow{}, err
		}
		r.ClaimedAt = &t
	}
	if lastHeartbeatStr.Valid {
		t, err := parseTime(lastHeartbeatStr.String)
		if err != nil {
			return shared.DispatchRow{}, err
		}
		r.LastHeartbeatAt = &t
	}
	if r.FrameID, err = uuid.Parse(frameIDStr); err != nil {
		return shared.DispatchRow{}, err
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
