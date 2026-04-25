// Package postgres is the Postgres implementation of queue.DispatchQueue.
//
// Port of rimsky/src/queue/postgres-queue.ts. The @blessed-invariant comment
// on Claim is load-bearing — preserve it verbatim on any refactor.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/queue"
	"github.com/fallguy/rimsky/core/shared"
)

// Queue is the Postgres-backed DispatchQueue.
type Queue struct {
	pool *pgxpool.Pool
}

// New returns a Queue bound to the given pool. The pool must already point at
// a database where the rimsky migrations have been applied.
func New(pool *pgxpool.Pool) *Queue {
	return &Queue{pool: pool}
}

// ensure Queue implements the interface.
var _ queue.DispatchQueue = (*Queue)(nil)

// Enqueue inserts or refreshes a dispatch row for the given node. On conflict
// (UNIQUE node_id) the row is updated ONLY if still unclaimed and already
// eligible — a claimed or future-dated row is left alone.
func (q *Queue) Enqueue(ctx context.Context, req queue.DispatchRequest) error {
	tags := req.ConcurrencyTags
	if tags == nil {
		tags = []string{}
	}
	_, err := q.pool.Exec(ctx,
		`INSERT INTO rimsky_dispatch (id, node_id, executor_name, concurrency_tags, enqueued_at)
		 VALUES (gen_random_uuid(), $1, $2, $3, $4)
		 ON CONFLICT (node_id) DO UPDATE
		   SET enqueued_at = EXCLUDED.enqueued_at,
		       executor_name = EXCLUDED.executor_name,
		       concurrency_tags = EXCLUDED.concurrency_tags
		   WHERE rimsky_dispatch.claimed_by IS NULL
		     AND rimsky_dispatch.enqueued_at <= NOW()`,
		req.NodeID, req.ExecutorName, tags, req.EnqueuedAt,
	)
	return err
}

// Claim atomically selects one ready dispatch row respecting tag limits.
//
// @blessed-invariant "running window extends exactly as long as the
// dispatch row is claimed"
//
// Tag-limit counts are computed from `rimsky_dispatch.claimed_by IS NOT
// NULL` — i.e. dispatch rows are the concurrency accounting primitive,
// not `rimsky_nodes.state='running'`. This invariant requires that:
//
//	(1) `nodes.state = running` is entered BEFORE `queue.Complete` is
//	    called, and
//	(2) `queue.Complete` is called AFTER the terminal outcome persisted
//	    the node's new state (fresh / stale / failed).
//
// In supervisor's tryClaim flow: `queue.Claim()` →
// (runner) `nodes.updateState("running", ...)` → handler → terminal
// outcome persists new state → `defer queue.Complete(...)`. Any refactor
// that widens the window between "runner flips node to running" and
// "queue.Claim returned" — or between "terminal outcome persisted" and
// "dispatch row deleted" — must preserve the property that a concurrent
// claimer sees the dispatch row as `claimed_by != null` for the entire
// time the node is actually running. Otherwise the tag limit
// under-counts and two nodes with `per-instance:X` can race in.
func (q *Queue) Claim(
	ctx context.Context,
	supervisorID string,
	accepts []string,
	limits map[string]int,
) (*shared.DispatchRow, error) {
	conn, err := q.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	// Rollback is a no-op if Commit ran first.
	defer func() { _ = tx.Rollback(ctx) }()

	// Find eligible dispatch rows (claimed_by NULL, executor_name in accepts,
	// enqueued_at <= NOW), oldest first, locking rows for update and skipping
	// any already locked by concurrent claimers.
	//
	// `LIMIT 100` bounds the working set so a backlog of 10k queued nodes
	// doesn't force us to FOR UPDATE every eligible row and buffer them into
	// memory. The inner tag-count + advisory-lock loop breaks out of this
	// batch on the first claimable row; if every candidate in the first 100
	// is tag-blocked we'll pick up the next batch on the next Claim() call,
	// which is what we want (backpressure).
	rows, err := tx.Query(ctx,
		`SELECT id, node_id, executor_name, concurrency_tags, enqueued_at, claimed_by, claimed_at
		   FROM rimsky_dispatch
		  WHERE claimed_by IS NULL
		    AND executor_name = ANY($1::text[])
		    AND enqueued_at <= NOW()
		  ORDER BY enqueued_at
		  LIMIT 100
		  FOR UPDATE SKIP LOCKED`,
		accepts,
	)
	if err != nil {
		return nil, err
	}

	type candidate struct {
		id              shared.UUID
		nodeID          shared.UUID
		executorName    string
		concurrencyTags []string
		enqueuedAt      time.Time
		claimedBy       *string
		claimedAt       *time.Time
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(
			&c.id, &c.nodeID, &c.executorName, &c.concurrencyTags,
			&c.enqueuedAt, &c.claimedBy, &c.claimedAt,
		); err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Track tag usage locally for this claim pass so we don't over-subscribe
	// a tag across multiple eligible rows in the same Claim() call.
	localTagCounts := map[string]int{}

	for _, row := range candidates {
		tags := row.concurrencyTags
		if tags == nil {
			tags = []string{}
		}

		// Under READ COMMITTED, a plain SELECT count(*) FROM rimsky_dispatch
		// WHERE claimed_by IS NOT NULL cannot block on another transaction's
		// pending claim UPDATE — so two concurrent Claim() calls against a
		// tag with limit=1 could each read active=0 and both proceed. To
		// serialize tag counting, take a per-tag transactional advisory lock
		// keyed on each tag the row carries BEFORE reading the count. The
		// lock is held for the rest of this transaction and released on
		// COMMIT / ROLLBACK. pg_advisory_xact_lock waits if another txn
		// holds the same key; once acquired the count query observes any
		// committed prior claim UPDATE.
		//
		// DEADLOCK SAFETY: sort tags before acquiring locks so any two
		// concurrent claims that share a tag subset acquire the shared locks
		// in the same global order (lexicographic). Without this, tags
		// ["x","y"] vs ["y","x"] acquired in row-order could deadlock —
		// pg detects it and ERRORs one side, forcing an unnecessary retry.
		sortedTags := append([]string(nil), tags...)
		sort.Strings(sortedTags)
		for _, tag := range sortedTags {
			if _, err := tx.Exec(ctx,
				`SELECT pg_advisory_xact_lock(hashtext($1))`,
				fmt.Sprintf("rimsky_tag:%s", tag),
			); err != nil {
				return nil, err
			}
		}

		blocked := false
		for _, tag := range sortedTags {
			limit, ok := limits[tag]
			if !ok {
				continue
			}
			// Re-read the active count for this tag now that we hold the
			// advisory lock. Only committed claims are visible, and no other
			// claimer holding this tag's lock can race us until we COMMIT.
			var committed int
			if err := tx.QueryRow(ctx,
				`SELECT count(*)::int AS active
				   FROM rimsky_dispatch d
				  WHERE d.claimed_by IS NOT NULL
				    AND $1 = ANY(d.concurrency_tags)`,
				tag,
			).Scan(&committed); err != nil {
				return nil, err
			}
			local := localTagCounts[tag]
			active := committed + local
			if active >= limit {
				blocked = true
				break
			}
		}
		if blocked {
			continue
		}

		var claimed shared.DispatchRow
		var tagsOut []string
		if err := tx.QueryRow(ctx,
			`UPDATE rimsky_dispatch
			    SET claimed_by = $1, claimed_at = NOW()
			  WHERE id = $2
			  RETURNING id, node_id, executor_name, concurrency_tags, enqueued_at, claimed_by, claimed_at`,
			supervisorID, row.id,
		).Scan(
			&claimed.ID, &claimed.NodeID, &claimed.ExecutorName, &tagsOut,
			&claimed.EnqueuedAt, &claimed.ClaimedBy, &claimed.ClaimedAt,
		); err != nil {
			return nil, err
		}
		if tagsOut == nil {
			tagsOut = []string{}
		}
		claimed.ConcurrencyTags = tagsOut

		// Update local counts for subsequent rows in this same pass.
		for _, tag := range tags {
			localTagCounts[tag]++
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &claimed, nil
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return nil, nil
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
// layer (kept for symmetry with the TS contract). Guarded on non-empty
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

// ListOrphanedClaims returns dispatch rows whose claimed_at is older than
// cutoff AND whose node is still in state='stale' (supervisor died between
// Claim and the state→running transition).
func (q *Queue) ListOrphanedClaims(ctx context.Context, cutoff time.Time) ([]shared.DispatchRow, error) {
	rows, err := q.pool.Query(ctx,
		`SELECT d.id, d.node_id, d.executor_name, d.concurrency_tags, d.enqueued_at,
		        d.claimed_by, d.claimed_at
		   FROM rimsky_dispatch d
		   JOIN rimsky_nodes n ON n.id = d.node_id
		  WHERE d.claimed_by IS NOT NULL
		    AND d.claimed_at < $1
		    AND n.state = 'stale'`,
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
			&r.ID, &r.NodeID, &r.ExecutorName, &r.ConcurrencyTags,
			&r.EnqueuedAt, &r.ClaimedBy, &r.ClaimedAt,
		); err != nil {
			return nil, err
		}
		if r.ConcurrencyTags == nil {
			r.ConcurrencyTags = []string{}
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
			    SET claimed_by = NULL, claimed_at = NULL
			  WHERE id = $1 AND claimed_by = $2`,
			dispatchID, expectedClaimedBy,
		)
		return err
	}
	_, err := q.pool.Exec(ctx,
		`UPDATE rimsky_dispatch
		    SET claimed_by = NULL, claimed_at = NULL
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
