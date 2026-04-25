package postgres

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/internal/pgtest"
	"github.com/fallguy/rimsky/core/queue"
	"github.com/fallguy/rimsky/core/shared"
)

// insertNode inserts a minimal template → instance → node chain and returns
// the node id. instance_id has a non-nullable FK with ON DELETE CASCADE, so
// we cannot shortcut the chain. state is written verbatim.
func insertNode(t *testing.T, pool *pgxpool.Pool, state string) shared.UUID {
	t.Helper()
	ctx := context.Background()

	templateID := uuid.New()
	instanceID := uuid.New()
	nodeID := uuid.New()

	spec, err := json.Marshal(map[string]any{})
	require.NoError(t, err)
	params, err := json.Marshal(map[string]any{})
	require.NoError(t, err)

	_, err = pool.Exec(ctx,
		`INSERT INTO rimsky_templates (id, name, version, spec) VALUES ($1, $2, $3, $4)`,
		templateID, "tpl-"+templateID.String(), "v1", spec,
	)
	require.NoError(t, err)

	_, err = pool.Exec(ctx,
		`INSERT INTO rimsky_instances (id, template_id, consumer_key, params) VALUES ($1, $2, $3, $4)`,
		instanceID, templateID, "ck-"+instanceID.String(), params,
	)
	require.NoError(t, err)

	_, err = pool.Exec(ctx,
		`INSERT INTO rimsky_nodes (id, instance_id, node_type, state, dependencies, concurrency_tags)
		 VALUES ($1, $2, $3, $4, '{}'::uuid[], '{}'::text[])`,
		nodeID, instanceID, "t", state,
	)
	require.NoError(t, err)

	return nodeID
}

func newQueueWithPool(t *testing.T) (*Queue, *pgxpool.Pool, func()) {
	t.Helper()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	return New(pool), pool, teardown
}

func TestClaim_ReturnsNilWhenEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q, _, teardown := newQueueWithPool(t)
	t.Cleanup(teardown)

	row, err := q.Claim(ctx, "sup-1", []string{"exec"}, map[string]int{})
	require.NoError(t, err)
	require.Nil(t, row)
}

func TestEnqueueAndClaim_Happy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q, pool, teardown := newQueueWithPool(t)
	t.Cleanup(teardown)

	nodeID := insertNode(t, pool, "stale")

	require.NoError(t, q.Enqueue(ctx, queue.DispatchRequest{
		NodeID:          nodeID,
		ExecutorName:    "ingest",
		ConcurrencyTags: []string{"a", "b"},
		EnqueuedAt:      time.Now(),
	}))

	row, err := q.Claim(ctx, "sup-1", []string{"ingest"}, map[string]int{})
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, nodeID, row.NodeID)
	require.Equal(t, "ingest", row.ExecutorName)
	require.ElementsMatch(t, []string{"a", "b"}, row.ConcurrencyTags)
	require.NotNil(t, row.ClaimedBy)
	require.Equal(t, "sup-1", *row.ClaimedBy)

	// GetClaimedBy reports "claimed_by" now.
	own, err := q.GetClaimedBy(ctx, row.ID)
	require.NoError(t, err)
	require.Equal(t, "claimed_by", own.Kind)
	require.Equal(t, "sup-1", own.SupervisorID)
}

func TestClaim_FiltersByAccepts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q, pool, teardown := newQueueWithPool(t)
	t.Cleanup(teardown)

	nodeID := insertNode(t, pool, "stale")
	require.NoError(t, q.Enqueue(ctx, queue.DispatchRequest{
		NodeID:       nodeID,
		ExecutorName: "ingest",
		EnqueuedAt:   time.Now(),
	}))

	row, err := q.Claim(ctx, "sup-1", []string{"other"}, map[string]int{})
	require.NoError(t, err)
	require.Nil(t, row, "should not claim row whose executor is not in accepts")

	row, err = q.Claim(ctx, "sup-1", []string{"ingest"}, map[string]int{})
	require.NoError(t, err)
	require.NotNil(t, row)
}

func TestClaim_RespectsEnqueuedAt_InFuture(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q, pool, teardown := newQueueWithPool(t)
	t.Cleanup(teardown)

	nodeID := insertNode(t, pool, "stale")
	require.NoError(t, q.Enqueue(ctx, queue.DispatchRequest{
		NodeID:       nodeID,
		ExecutorName: "ingest",
		EnqueuedAt:   time.Now().Add(1 * time.Hour),
	}))

	row, err := q.Claim(ctx, "sup-1", []string{"ingest"}, map[string]int{})
	require.NoError(t, err)
	require.Nil(t, row, "future-dated enqueue must not be claimable yet")
}

func TestClaim_RespectsTagLimits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q, pool, teardown := newQueueWithPool(t)
	t.Cleanup(teardown)

	n1 := insertNode(t, pool, "stale")
	n2 := insertNode(t, pool, "stale")

	require.NoError(t, q.Enqueue(ctx, queue.DispatchRequest{
		NodeID: n1, ExecutorName: "ingest",
		ConcurrencyTags: []string{"per-instance:X"},
		EnqueuedAt:      time.Now(),
	}))
	require.NoError(t, q.Enqueue(ctx, queue.DispatchRequest{
		NodeID: n2, ExecutorName: "ingest",
		ConcurrencyTags: []string{"per-instance:X"},
		EnqueuedAt:      time.Now().Add(10 * time.Millisecond),
	}))

	limits := map[string]int{"per-instance:X": 1}

	row1, err := q.Claim(ctx, "sup-1", []string{"ingest"}, limits)
	require.NoError(t, err)
	require.NotNil(t, row1)

	// second claim blocked by tag limit (n1 is still claimed).
	row2, err := q.Claim(ctx, "sup-1", []string{"ingest"}, limits)
	require.NoError(t, err)
	require.Nil(t, row2, "tag limit=1 should block second claim while first is live")

	// After completing the first, the second becomes claimable.
	require.NoError(t, q.Complete(ctx, row1.ID, ""))
	row3, err := q.Claim(ctx, "sup-1", []string{"ingest"}, limits)
	require.NoError(t, err)
	require.NotNil(t, row3)
}

func TestClaim_TwoConcurrentClaimsSerializedByAdvisoryLock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q, pool, teardown := newQueueWithPool(t)
	t.Cleanup(teardown)

	// Single eligible row whose tag limit is 1.
	nodeID := insertNode(t, pool, "stale")
	require.NoError(t, q.Enqueue(ctx, queue.DispatchRequest{
		NodeID: nodeID, ExecutorName: "ingest",
		ConcurrencyTags: []string{"per-instance:Y"},
		EnqueuedAt:      time.Now(),
	}))

	limits := map[string]int{"per-instance:Y": 1}

	// Launch two claims in parallel; only one may succeed due to the
	// advisory lock + tag-count serialization.
	var wg sync.WaitGroup
	results := make([]*shared.DispatchRow, 2)
	errs := make([]error, 2)
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func(idx int) {
			defer wg.Done()
			r, err := q.Claim(ctx, "sup-"+string(rune('A'+idx)), []string{"ingest"}, limits)
			results[idx] = r
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	require.NoError(t, errs[0])
	require.NoError(t, errs[1])

	winners := 0
	for _, r := range results {
		if r != nil {
			winners++
		}
	}
	require.Equal(t, 1, winners, "exactly one claimer should win under tag limit=1")

	_ = pool // keep pool reachable
}

func TestReleaseClaim_Guarded_NoOpOnMismatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q, pool, teardown := newQueueWithPool(t)
	t.Cleanup(teardown)

	nodeID := insertNode(t, pool, "stale")
	require.NoError(t, q.Enqueue(ctx, queue.DispatchRequest{
		NodeID: nodeID, ExecutorName: "ingest", EnqueuedAt: time.Now(),
	}))
	row, err := q.Claim(ctx, "sup-winner", []string{"ingest"}, map[string]int{})
	require.NoError(t, err)
	require.NotNil(t, row)

	// Guarded release by a DIFFERENT supervisor must be a no-op.
	require.NoError(t, q.ReleaseClaim(ctx, row.ID, "sup-ghost"))

	own, err := q.GetClaimedBy(ctx, row.ID)
	require.NoError(t, err)
	require.Equal(t, "claimed_by", own.Kind)
	require.Equal(t, "sup-winner", own.SupervisorID)

	// Guarded release by the correct supervisor clears the claim.
	require.NoError(t, q.ReleaseClaim(ctx, row.ID, "sup-winner"))
	own, err = q.GetClaimedBy(ctx, row.ID)
	require.NoError(t, err)
	require.Equal(t, "unclaimed", own.Kind)
}

func TestListOrphanedClaims_FiltersOnNodeStaleState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q, pool, teardown := newQueueWithPool(t)
	t.Cleanup(teardown)

	// One node stays stale (orphan-eligible), one flips to running (excluded).
	nStale := insertNode(t, pool, "stale")
	nRunning := insertNode(t, pool, "stale")

	require.NoError(t, q.Enqueue(ctx, queue.DispatchRequest{
		NodeID: nStale, ExecutorName: "ingest", EnqueuedAt: time.Now(),
	}))
	require.NoError(t, q.Enqueue(ctx, queue.DispatchRequest{
		NodeID: nRunning, ExecutorName: "ingest", EnqueuedAt: time.Now().Add(1 * time.Millisecond),
	}))

	r1, err := q.Claim(ctx, "sup-1", []string{"ingest"}, map[string]int{})
	require.NoError(t, err)
	require.NotNil(t, r1)
	r2, err := q.Claim(ctx, "sup-1", []string{"ingest"}, map[string]int{})
	require.NoError(t, err)
	require.NotNil(t, r2)

	// Flip the second node to running.
	_, err = pool.Exec(ctx,
		`UPDATE rimsky_nodes SET state='running' WHERE id = $1`, nRunning,
	)
	require.NoError(t, err)

	// Backdate claimed_at so both are past the cutoff.
	_, err = pool.Exec(ctx,
		`UPDATE rimsky_dispatch SET claimed_at = NOW() - INTERVAL '10 minutes'`,
	)
	require.NoError(t, err)

	orphans, err := q.ListOrphanedClaims(ctx, time.Now().Add(-1*time.Minute))
	require.NoError(t, err)
	require.Len(t, orphans, 1)
	require.Equal(t, nStale, orphans[0].NodeID)
}

func TestGetClaimedBy_ThreeKinds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q, pool, teardown := newQueueWithPool(t)
	t.Cleanup(teardown)

	// not_found
	own, err := q.GetClaimedBy(ctx, uuid.New())
	require.NoError(t, err)
	require.Equal(t, "not_found", own.Kind)

	// unclaimed
	nodeID := insertNode(t, pool, "stale")
	require.NoError(t, q.Enqueue(ctx, queue.DispatchRequest{
		NodeID: nodeID, ExecutorName: "ingest", EnqueuedAt: time.Now(),
	}))
	var dispatchID shared.UUID
	err = pool.QueryRow(ctx, `SELECT id FROM rimsky_dispatch WHERE node_id = $1`, nodeID).Scan(&dispatchID)
	require.NoError(t, err)

	own, err = q.GetClaimedBy(ctx, dispatchID)
	require.NoError(t, err)
	require.Equal(t, "unclaimed", own.Kind)

	// claimed_by
	row, err := q.Claim(ctx, "sup-xyz", []string{"ingest"}, map[string]int{})
	require.NoError(t, err)
	require.NotNil(t, row)

	own, err = q.GetClaimedBy(ctx, row.ID)
	require.NoError(t, err)
	require.Equal(t, "claimed_by", own.Kind)
	require.Equal(t, "sup-xyz", own.SupervisorID)
}

func TestComplete_GuardedDoesNotDeleteFreshClaim(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q, pool, teardown := newQueueWithPool(t)
	t.Cleanup(teardown)

	nodeID := insertNode(t, pool, "stale")
	require.NoError(t, q.Enqueue(ctx, queue.DispatchRequest{
		NodeID: nodeID, ExecutorName: "ingest", EnqueuedAt: time.Now(),
	}))
	row, err := q.Claim(ctx, "sup-fresh", []string{"ingest"}, map[string]int{})
	require.NoError(t, err)
	require.NotNil(t, row)

	// A stale supervisor's guarded Complete must NOT delete the row.
	require.NoError(t, q.Complete(ctx, row.ID, "sup-stale"))

	own, err := q.GetClaimedBy(ctx, row.ID)
	require.NoError(t, err)
	require.Equal(t, "claimed_by", own.Kind)
	require.Equal(t, "sup-fresh", own.SupervisorID)

	// The rightful owner's guarded Complete deletes it.
	require.NoError(t, q.Complete(ctx, row.ID, "sup-fresh"))
	own, err = q.GetClaimedBy(ctx, row.ID)
	require.NoError(t, err)
	require.Equal(t, "not_found", own.Kind)
}
