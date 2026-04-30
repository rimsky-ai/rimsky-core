// Package postgres is the Postgres implementation of queue.DispatchQueue.
//
// Under the stores redesign (spec §7), this package owns rimsky_dispatch
// only. The §7.3 atomic-acquisition transaction is orchestrated by
// core/supervisor/runner.go; the helpers below (SelectCandidates,
// ClaimDispatchRow, TakeNamedLockAdvisory) are the building blocks the
// runner calls inside the single pgx.Tx that brackets candidate selection,
// per-named-lock advisory locking, the claimant-guarded dispatch UPDATE,
// region re-evaluation, and lock-holder inserts.
//
// The previous "do everything in Claim()" entry point is gone: per-row
// gating data (concurrency_tags) was removed from the schema; per-spec
// gating data (named/region/claim locks) lives in the in-memory template
// registry which this package does not import. Splitting the work into
// helpers lets the supervisor's runner do the in-Go eligibility checks
// (§7.3 step 2) between candidate selection and the claim UPDATE.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/queue"
	"github.com/fallguy/rimsky/core/shared"
)

// defaultCandidateLimit caps the candidate batch returned by
// SelectCandidates when the caller passes Limit==0. The spec's nominal
// step-1 SQL uses LIMIT 1; a small batch is exposed as the default so
// the runner can iterate to the next eligible candidate (skipping ones
// that fail in-Go lock eligibility) without re-running the SELECT.
const defaultCandidateLimit = 100

// Queue is the Postgres-backed DispatchQueue.
type Queue struct {
	pool *pgxpool.Pool
}

// New returns a Queue bound to the given pool. The pool must already point at
// a database where the rimsky migrations have been applied.
func New(pool *pgxpool.Pool) *Queue {
	return &Queue{pool: pool}
}

// Pool returns the underlying connection pool. Callers that need to start
// the §7.3 acquisition transaction (the supervisor's runner) use this so
// the rest of the helper surface can take an open pgx.Tx.
func (q *Queue) Pool() *pgxpool.Pool { return q.pool }

// ensure Queue implements the interface.
var _ queue.DispatchQueue = (*Queue)(nil)

// Enqueue inserts or refreshes a dispatch row for the given node. On conflict
// (UNIQUE node_id) the row is updated ONLY if still unclaimed and already
// eligible — a claimed or future-dated row is left alone. RequiredStores is
// denormalised from the template at enqueue time and drives the §6.2
// supervisor-pool specialisation predicate.
func (q *Queue) Enqueue(ctx context.Context, req queue.DispatchRequest) error {
	stores := req.RequiredStores
	if stores == nil {
		stores = []string{}
	}
	executor := nullableText(req.ExecutorName)
	if req.FrameID == (shared.UUID{}) {
		return fmt.Errorf("postgres.Enqueue: frame_id required (per blessed-invariant 19) for node %s", req.NodeID)
	}
	_, err := q.pool.Exec(ctx,
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

// SelectCandidates is the §7.3 step 1 candidate-selection helper.
//
// SQL: SELECT FROM rimsky_dispatch WHERE claimed_by IS NULL AND
// required_stores <@ accepted_stores AND
// (executor_name = ANY(accepted_executors) OR executor_name IS NULL)
// ORDER BY enqueued_at FOR UPDATE SKIP LOCKED LIMIT $limit.
//
// The caller MUST hold an open transaction; rows returned have their
// per-row locks held until the tx commits or rolls back. The runner
// iterates these candidates, evaluates the in-Go lock eligibility hints
// (§7.3 step 2) for each, and on the first eligible one proceeds to step 3
// (advisory locks + ClaimDispatchRow).
//
// The IS NULL branch of the executor predicate is load-bearing: native
// (claim-only) nodes enqueue with executor_name IS NULL and are
// run by the supervisor's omnibus runner without a separate executor
// process.
func (q *Queue) SelectCandidates(
	ctx context.Context, tx pgx.Tx, req queue.SelectCandidatesRequest,
) ([]queue.Candidate, error) {
	if tx == nil {
		return nil, errors.New("postgres.SelectCandidates: tx required")
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

	rows, err := tx.Query(ctx,
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

	var out []queue.Candidate
	for rows.Next() {
		var (
			c            queue.Candidate
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

// ClaimDispatchRow is the §7.3 step 3c claimant-guarded UPDATE.
//
// Sets claimed_by=supervisorID, claimed_at=now(), last_heartbeat_at=now()
// for the given dispatch row, but only when claimed_by IS NULL.
//
// Returns claimed=true on a single-row update; claimed=false when the row
// was already claimed by someone else. Under FOR UPDATE SKIP LOCKED inside
// the same tx the false branch should not occur; the guard is the
// invariant per spec §7.3 step 3c.
//
// The dispatch row is the running-window primitive: tag-limit / named-
// lock counts read rimsky_dispatch.claimed_by IS NOT NULL joined against
// rimsky_lock_holders.expires_at > now(), so the running window must
// extend exactly as long as the claim holds. The caller (runner) commits
// this UPDATE alongside the lock-holder inserts in the same tx so claim
// and lock-holder rows go visible atomically. (Blessed-invariant 2.)
func (q *Queue) ClaimDispatchRow(
	ctx context.Context, tx pgx.Tx, dispatchID shared.UUID, supervisorID string,
) (bool, error) {
	if tx == nil {
		return false, errors.New("postgres.ClaimDispatchRow: tx required")
	}
	cmd, err := tx.Exec(ctx,
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

// TakeNamedLockAdvisory takes a transactional advisory lock keyed on
// "rimsky_lock:" + lockName. Spec §7.3 step 3b: under
// pg_advisory_xact_lock, two supervisors trying to acquire the same named
// lock serialize on this call before re-counting holders.
//
// The lock is released automatically on COMMIT / ROLLBACK of tx. The
// caller is responsible for sorting lock names per blessed-invariant 3
// before taking multiple locks in one tx — without sorting, two callers
// holding different orderings of the same set deadlock.
func TakeNamedLockAdvisory(ctx context.Context, tx pgx.Tx, lockName string) error {
	if tx == nil {
		return errors.New("postgres.TakeNamedLockAdvisory: tx required")
	}
	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtext($1))`,
		fmt.Sprintf("rimsky_lock:%s", lockName),
	); err != nil {
		return fmt.Errorf("postgres.TakeNamedLockAdvisory(%q): %w", lockName, err)
	}
	return nil
}

// TakeRegionAdvisory takes a transactional advisory lock keyed on
// "rimsky_region:" + storeName + ":" + regionData. Used by
// runner_acquire to serialize concurrent acquisitions targeting the
// same (store, region) pair so that two supervisors evaluating
// region-conflict against an uncommitted INSERT cannot both pass —
// preserves single-writer-per-region (invariant 4b) for non-pick-policy
// regional claims, where the store's FOR UPDATE SKIP LOCKED predicate
// is unavailable.
//
// The lock is released automatically on COMMIT / ROLLBACK of tx. Per
// blessed-invariant 3, callers must take all advisory locks (named
// AND region) in deterministic sort order — runner_acquire sorts the
// spec slice via sortLockSpecs before any advisory call.
func TakeRegionAdvisory(ctx context.Context, tx pgx.Tx, storeName string, regionData []byte) error {
	if tx == nil {
		return errors.New("postgres.TakeRegionAdvisory: tx required")
	}
	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtext($1))`,
		fmt.Sprintf("rimsky_region:%s:%s", storeName, string(regionData)),
	); err != nil {
		return fmt.Errorf("postgres.TakeRegionAdvisory(%q): %w", storeName, err)
	}
	return nil
}

// Complete deletes the dispatch row. If expectedClaimedBy is non-empty, the
// delete is guarded — mismatches are a no-op (another supervisor's live claim
// stays intact).
func (q *Queue) Complete(ctx context.Context, dispatchID shared.UUID, expectedClaimedBy string) error {
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

// Fail deletes the dispatch row. reason is currently logged/unused at the SQL
// layer (kept for symmetry with the contract). Guarded on non-empty
// expectedClaimedBy.
func (q *Queue) Fail(
	ctx context.Context,
	dispatchID shared.UUID,
	_ string,
	expectedClaimedBy string,
) error {
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

// RemoveForNode deletes a pending dispatch row by node_id. Guarded on
// non-empty expectedClaimedBy.
func (q *Queue) RemoveForNode(ctx context.Context, nodeID shared.UUID, expectedClaimedBy string) error {
	if expectedClaimedBy != "" {
		_, err := q.pool.Exec(ctx,
			`DELETE FROM rimsky_dispatch WHERE node_id = $1 AND claimed_by = $2`,
			nodeID, expectedClaimedBy,
		)
		return err
	}
	_, err := q.pool.Exec(ctx,
		`DELETE FROM rimsky_dispatch WHERE node_id = $1`, nodeID,
	)
	return err
}

// ListOrphanedClaims returns dispatch rows whose last_heartbeat_at is older
// than cutoff and claimed_by IS NOT NULL. Spec §7.5 step 1: the redesign
// switched the predicate column from claimed_at to last_heartbeat_at so a
// long-running but heartbeating supervisor is not reaped.
//
// The previous implementation also filtered on `rimsky_nodes.state = 'stale'`
// to avoid releasing a claim under a still-running node. Under the redesign
// the heartbeat tick (§7.5) refreshes last_heartbeat_at while the node is
// running, so a running node's claim cannot satisfy the predicate;
// dropping the join keeps the sweep aligned with the spec text.
func (q *Queue) ListOrphanedClaims(ctx context.Context, cutoff time.Time) ([]shared.DispatchRow, error) {
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

// ReleaseClaim nulls claim fields. Guarded release (non-empty
// expectedClaimedBy): mismatch is a no-op — a fresh supervisor's live claim
// from a stale sweep must stay intact.
func (q *Queue) ReleaseClaim(ctx context.Context, dispatchID shared.UUID, expectedClaimedBy string) error {
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

// GetClaimedBy returns current ownership of a dispatch row (for the
// verify-before-run invariant).
func (q *Queue) GetClaimedBy(ctx context.Context, dispatchID shared.UUID) (queue.ClaimOwnership, error) {
	var claimedBy *string
	err := q.pool.QueryRow(ctx,
		`SELECT claimed_by FROM rimsky_dispatch WHERE id = $1`,
		dispatchID,
	).Scan(&claimedBy)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return queue.ClaimOwnership{Kind: "not_found"}, nil
		}
		return queue.ClaimOwnership{}, err
	}
	if claimedBy == nil {
		return queue.ClaimOwnership{Kind: "unclaimed"}, nil
	}
	return queue.ClaimOwnership{Kind: "claimed_by", SupervisorID: *claimedBy}, nil
}

// RefreshHeartbeat extends rimsky_dispatch.last_heartbeat_at to NOW() for
// every row claimed by supervisorID. Spec §7.5 — paired with the
// lock-holder heartbeat extend (in core/store/lockholders.go); the
// dispatch sweep predicate filters on this column so a heartbeating
// supervisor is not reaped.
func (q *Queue) RefreshHeartbeat(ctx context.Context, supervisorID string) error {
	_, err := q.pool.Exec(ctx,
		`UPDATE rimsky_dispatch SET last_heartbeat_at = NOW() WHERE claimed_by = $1`,
		supervisorID,
	)
	if err != nil {
		return fmt.Errorf("postgres.RefreshHeartbeat: %w", err)
	}
	return nil
}

// nullableText converts an empty string to a SQL NULL marker so a
// native (claim-only) node enqueues with executor_name IS NULL.
func nullableText(s string) any {
	if s == "" {
		return nil
	}
	return s
}
