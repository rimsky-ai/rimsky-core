// queue.go is the Postgres implementation of persistence.Queue.
//
// Under the stores redesign (spec §7), this surface owns rimsky_dispatch
// only. The §7.3 atomic-acquisition transaction is orchestrated by
// core/supervisor/runner.go; the helpers below (SelectCandidates,
// ClaimDispatchRow) are the building blocks the runner calls inside the
// single persistence.Tx that brackets candidate selection, per-named-lock
// advisory locking, the claimant-guarded dispatch UPDATE, region
// re-evaluation, and lock-holder inserts.
//
// @blessed-invariant 2: dispatch claim brackets the running window.
package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/persistence"
	"github.com/fallguy/rimsky/core/shared"
)

// defaultCandidateLimit caps the candidate batch returned by
// SelectCandidates when the caller passes Limit==0.
const defaultCandidateLimit = 100

type queueImpl struct {
	pool *pgxpool.Pool
}

func newQueue(pool *pgxpool.Pool) *queueImpl { return &queueImpl{pool: pool} }

var _ persistence.Queue = (*queueImpl)(nil)

// q returns the right querier for tx. Same convention as storeImpl.q.
func (q *queueImpl) q(tx persistence.Tx) querier {
	if tx == nil {
		return q.pool
	}
	t, ok := tx.(*pgTx)
	if !ok {
		panic(fmt.Sprintf("postgres.queueImpl.q: persistence.Tx is not a postgres tx: %T", tx))
	}
	return t.tx
}

// Enqueue is the auto-commit entry point. EnqueueInTx with tx=nil falls
// back to the pool via q(nil).
func (q *queueImpl) Enqueue(ctx context.Context, req persistence.DispatchRequest) error {
	return q.EnqueueInTx(ctx, req, nil)
}

// EnqueueInTx inserts or refreshes a dispatch row inside the caller's tx
// (or auto-commits when tx == nil). On UNIQUE(node_id) conflict the row
// is updated only when still unclaimed and already eligible — a claimed
// or future-dated row is left alone.
func (q *queueImpl) EnqueueInTx(ctx context.Context, req persistence.DispatchRequest, tx persistence.Tx) error {
	stores := req.RequiredStores
	if stores == nil {
		stores = []string{}
	}
	executor := nullableText(req.ExecutorName)
	if req.FrameID == (shared.UUID{}) {
		return fmt.Errorf("postgres.Enqueue: frame_id required (per blessed-invariant 19) for node %s", req.NodeID)
	}
	_, err := q.q(tx).Exec(ctx,
		`INSERT INTO rimsky_dispatch (id, node_id, executor_name, required_stores, enqueued_at, frame_id)
		 VALUES (gen_random_uuid(), $1, $2, $3, $4, $5)
		 ON CONFLICT (node_id) DO UPDATE
		   SET enqueued_at = EXCLUDED.enqueued_at,
		       executor_name = EXCLUDED.executor_name,
		       required_stores = EXCLUDED.required_stores,
		       frame_id = EXCLUDED.frame_id
		   WHERE rimsky_dispatch.claimed_by IS NULL
		     AND rimsky_dispatch.enqueued_at <= NOW()`,
		req.NodeID, executor, stores, req.EnqueuedAt, req.FrameID,
	)
	return err
}

// SelectCandidates is the §7.3 step 1 candidate-selection helper. The
// caller MUST hold an open tx; rows returned have their per-row locks
// held until tx commits or rolls back.
func (q *queueImpl) SelectCandidates(
	ctx context.Context, tx persistence.Tx, req persistence.SelectCandidatesRequest,
) ([]persistence.Candidate, error) {
	if tx == nil {
		return nil, errors.New("postgres.SelectCandidates: tx required")
	}
	pgT, err := unwrapTx(tx)
	if err != nil {
		return nil, fmt.Errorf("postgres.SelectCandidates: %w", err)
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

	rows, err := pgT.Query(ctx,
		`SELECT d.id, d.node_id, n.node_type, d.executor_name, d.required_stores, d.enqueued_at, d.frame_id
		   FROM rimsky_dispatch d
		   JOIN rimsky_nodes n ON n.id = d.node_id
		  WHERE d.claimed_by IS NULL
		    AND d.required_stores <@ $1::text[]
		    AND (d.executor_name = ANY($2::text[]) OR d.executor_name IS NULL)
		    AND d.enqueued_at <= NOW()
		  ORDER BY d.enqueued_at
		  LIMIT $3
		  FOR UPDATE SKIP LOCKED`,
		acceptedStores, acceptedExecutors, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("postgres.SelectCandidates: %w", err)
	}
	defer rows.Close()

	var out []persistence.Candidate
	for rows.Next() {
		var (
			c            persistence.Candidate
			executorName *string
		)
		if err := rows.Scan(
			&c.DispatchID, &c.NodeID, &c.NodeType,
			&executorName, &c.RequiredStores, &c.EnqueuedAt, &c.FrameID,
		); err != nil {
			return nil, fmt.Errorf("postgres.SelectCandidates: scan: %w", err)
		}
		if executorName != nil {
			c.ExecutorName = *executorName
		}
		if c.RequiredStores == nil {
			c.RequiredStores = []string{}
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres.SelectCandidates: rows: %w", err)
	}
	return out, nil
}

// ClaimDispatchRow — @blessed-invariant 2.
func (q *queueImpl) ClaimDispatchRow(
	ctx context.Context, tx persistence.Tx, dispatchID shared.UUID, supervisorID string,
) (bool, error) {
	if tx == nil {
		return false, errors.New("postgres.ClaimDispatchRow: tx required")
	}
	cmd, err := q.q(tx).Exec(ctx,
		`UPDATE rimsky_dispatch
		    SET claimed_by = $1, claimed_at = NOW(), last_heartbeat_at = NOW()
		  WHERE id = $2 AND claimed_by IS NULL`,
		supervisorID, dispatchID,
	)
	if err != nil {
		return false, fmt.Errorf("postgres.ClaimDispatchRow: %w", err)
	}
	return cmd.RowsAffected() == 1, nil
}

func (q *queueImpl) Complete(ctx context.Context, dispatchID shared.UUID, expectedClaimedBy string) error {
	if expectedClaimedBy != "" {
		_, err := q.pool.Exec(ctx,
			`DELETE FROM rimsky_dispatch WHERE id = $1 AND claimed_by = $2`,
			dispatchID, expectedClaimedBy,
		)
		return err
	}
	_, err := q.pool.Exec(ctx,
		`DELETE FROM rimsky_dispatch WHERE id = $1`, dispatchID,
	)
	return err
}

func (q *queueImpl) RemoveForNode(ctx context.Context, nodeID shared.UUID, expectedClaimedBy string) error {
	return q.RemoveForNodeInTx(ctx, nodeID, expectedClaimedBy, nil)
}

// RemoveForNodeInTx mirrors RemoveForNode but executes through the
// caller's tx (or auto-commit when tx == nil).
func (q *queueImpl) RemoveForNodeInTx(ctx context.Context, nodeID shared.UUID, expectedClaimedBy string, tx persistence.Tx) error {
	if expectedClaimedBy != "" {
		_, err := q.q(tx).Exec(ctx,
			`DELETE FROM rimsky_dispatch WHERE node_id = $1 AND claimed_by = $2`,
			nodeID, expectedClaimedBy,
		)
		return err
	}
	_, err := q.q(tx).Exec(ctx,
		`DELETE FROM rimsky_dispatch WHERE node_id = $1`, nodeID,
	)
	return err
}

// ListOrphanedClaims returns dispatch rows whose last_heartbeat_at is
// older than cutoff. @blessed-invariant 6 (5× heartbeat cutoff).
func (q *queueImpl) ListOrphanedClaims(ctx context.Context, cutoff time.Time) ([]shared.DispatchRow, error) {
	rows, err := q.pool.Query(ctx,
		`SELECT id, node_id, executor_name, required_stores, enqueued_at,
		        claimed_by, claimed_at, last_heartbeat_at, frame_id
		   FROM rimsky_dispatch
		  WHERE claimed_by IS NOT NULL
		    AND last_heartbeat_at < $1`,
		cutoff,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []shared.DispatchRow
	for rows.Next() {
		var r shared.DispatchRow
		if err := rows.Scan(
			&r.ID, &r.NodeID, &r.ExecutorName, &r.RequiredStores,
			&r.EnqueuedAt, &r.ClaimedBy, &r.ClaimedAt, &r.LastHeartbeatAt, &r.FrameID,
		); err != nil {
			return nil, err
		}
		if r.RequiredStores == nil {
			r.RequiredStores = []string{}
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ReleaseClaim nulls claim fields. @blessed-invariant 4.
func (q *queueImpl) ReleaseClaim(ctx context.Context, dispatchID shared.UUID, expectedClaimedBy string) error {
	if expectedClaimedBy != "" {
		_, err := q.pool.Exec(ctx,
			`UPDATE rimsky_dispatch
			    SET claimed_by = NULL, claimed_at = NULL, last_heartbeat_at = NULL
			  WHERE id = $1 AND claimed_by = $2`,
			dispatchID, expectedClaimedBy,
		)
		return err
	}
	_, err := q.pool.Exec(ctx,
		`UPDATE rimsky_dispatch
		    SET claimed_by = NULL, claimed_at = NULL, last_heartbeat_at = NULL
		  WHERE id = $1`,
		dispatchID,
	)
	return err
}

// GetDispatchNode returns the node_id + ownership for a dispatch row.
// Used by the supervisor's §12.5 attributes-callback auth path.
func (q *queueImpl) GetDispatchNode(ctx context.Context, dispatchID shared.UUID) (shared.UUID, persistence.ClaimOwnership, error) {
	var (
		nodeID    shared.UUID
		claimedBy *string
	)
	err := q.pool.QueryRow(ctx,
		`SELECT node_id, claimed_by FROM rimsky_dispatch WHERE id = $1`,
		dispatchID,
	).Scan(&nodeID, &claimedBy)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return shared.UUID{}, persistence.ClaimOwnership{Kind: "not_found"}, nil
		}
		return shared.UUID{}, persistence.ClaimOwnership{}, err
	}
	if claimedBy == nil {
		return nodeID, persistence.ClaimOwnership{Kind: "unclaimed"}, nil
	}
	return nodeID, persistence.ClaimOwnership{Kind: "claimed_by", SupervisorID: *claimedBy}, nil
}

// GetClaimedBy — @blessed-invariant 5: verify-before-run.
func (q *queueImpl) GetClaimedBy(ctx context.Context, dispatchID shared.UUID) (persistence.ClaimOwnership, error) {
	var claimedBy *string
	err := q.pool.QueryRow(ctx,
		`SELECT claimed_by FROM rimsky_dispatch WHERE id = $1`,
		dispatchID,
	).Scan(&claimedBy)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return persistence.ClaimOwnership{Kind: "not_found"}, nil
		}
		return persistence.ClaimOwnership{}, err
	}
	if claimedBy == nil {
		return persistence.ClaimOwnership{Kind: "unclaimed"}, nil
	}
	return persistence.ClaimOwnership{Kind: "claimed_by", SupervisorID: *claimedBy}, nil
}

func (q *queueImpl) RefreshHeartbeat(ctx context.Context, supervisorID string) error {
	_, err := q.pool.Exec(ctx,
		`UPDATE rimsky_dispatch SET last_heartbeat_at = NOW() WHERE claimed_by = $1`,
		supervisorID,
	)
	if err != nil {
		return fmt.Errorf("postgres.RefreshHeartbeat: %w", err)
	}
	return nil
}

// nullableText converts an empty string to SQL NULL.
func nullableText(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ListLive returns currently-live dispatch rows for the observability
// browse endpoint. Cursor pagination over (enqueued_at DESC, id DESC).
func (q *queueImpl) ListLive(ctx context.Context, filter persistence.DispatchListFilter, pag persistence.ListPagination) (persistence.PaginatedListResult[shared.DispatchRow], error) {
	limit := pag.Limit
	if limit <= 0 {
		limit = 50
	}
	var cursorEnq *time.Time
	var cursorID *shared.UUID
	if pag.Cursor != "" {
		oc, id, err := decodeDispatchCursor(pag.Cursor)
		if err != nil {
			return persistence.PaginatedListResult[shared.DispatchRow]{}, fmt.Errorf("postgres.ListLive: bad cursor: %w", err)
		}
		cursorEnq = &oc
		cursorID = &id
	}
	var stateClaimed any
	switch filter.State {
	case "pending":
		stateClaimed = false
	case "claimed":
		stateClaimed = true
	}
	executor := nullableText(filter.ExecutorName)
	var instanceID any
	if filter.InstanceID != nil {
		instanceID = *filter.InstanceID
	}
	rows, err := q.pool.Query(ctx,
		`SELECT d.id, d.node_id, d.executor_name, d.required_stores, d.enqueued_at,
		        d.claimed_by, d.claimed_at, d.last_heartbeat_at, d.frame_id
		   FROM rimsky_dispatch d
		   LEFT JOIN rimsky_nodes n ON n.id = d.node_id
		  WHERE ($1::bool IS NULL OR (d.claimed_by IS NOT NULL) = $1)
		    AND ($2::text IS NULL OR d.executor_name = $2)
		    AND ($3::uuid IS NULL OR n.instance_id = $3)
		    AND ($4::timestamptz IS NULL OR (d.enqueued_at, d.id) < ($4, $5))
		  ORDER BY d.enqueued_at DESC, d.id DESC
		  LIMIT $6`,
		stateClaimed, executor, instanceID,
		nullableTime(cursorEnq), nullableUUID(cursorID),
		limit,
	)
	if err != nil {
		return persistence.PaginatedListResult[shared.DispatchRow]{}, err
	}
	defer rows.Close()
	var out []shared.DispatchRow
	for rows.Next() {
		var r shared.DispatchRow
		if err := rows.Scan(
			&r.ID, &r.NodeID, &r.ExecutorName, &r.RequiredStores,
			&r.EnqueuedAt, &r.ClaimedBy, &r.ClaimedAt, &r.LastHeartbeatAt, &r.FrameID,
		); err != nil {
			return persistence.PaginatedListResult[shared.DispatchRow]{}, err
		}
		if r.RequiredStores == nil {
			r.RequiredStores = []string{}
		}
		out = append(out, r)
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
	var stateClaimed any
	switch filter.State {
	case "pending":
		stateClaimed = false
	case "claimed":
		stateClaimed = true
	}
	executor := nullableText(filter.ExecutorName)
	var instanceID any
	if filter.InstanceID != nil {
		instanceID = *filter.InstanceID
	}
	var n int
	err := q.pool.QueryRow(ctx,
		`SELECT COUNT(*)
		   FROM rimsky_dispatch d
		   LEFT JOIN rimsky_nodes n ON n.id = d.node_id
		  WHERE ($1::bool IS NULL OR (d.claimed_by IS NOT NULL) = $1)
		    AND ($2::text IS NULL OR d.executor_name = $2)
		    AND ($3::uuid IS NULL OR n.instance_id = $3)`,
		stateClaimed, executor, instanceID,
	).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// ---- dispatch cursor encoding ----

func encodeDispatchCursor(enqueued time.Time, id shared.UUID) string {
	c := struct {
		E time.Time   `json:"e"`
		I shared.UUID `json:"i"`
	}{E: enqueued, I: id}
	b, _ := json.Marshal(c)
	return base64.StdEncoding.EncodeToString(b)
}

func decodeDispatchCursor(s string) (time.Time, shared.UUID, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, shared.UUID{}, err
	}
	var c struct {
		E time.Time   `json:"e"`
		I shared.UUID `json:"i"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return time.Time{}, shared.UUID{}, err
	}
	return c.E, c.I, nil
}

func nullableUUID(p *shared.UUID) any {
	if p == nil {
		return nil
	}
	return *p
}
