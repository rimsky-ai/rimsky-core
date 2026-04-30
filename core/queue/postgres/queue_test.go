package postgres

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/internal/pgtest"
	"github.com/fallguy/rimsky/core/queue"
	"github.com/fallguy/rimsky/core/shared"
)

// nodeFrameMap is a per-test-process map from node id → frame id. Tests
// build dispatch rows from node ids and call queue.Enqueue, which now
// requires DispatchRequest.FrameID. To avoid threading a frame id through
// every call site, insertNode seeds a running rimsky_frames row for each
// node's instance and stores the frame id in this map; testFrameID(nodeID)
// returns that frame id for plugging into DispatchRequest.
var (
	nodeFrameMu  sync.Mutex
	nodeFrameMap = map[shared.UUID]shared.UUID{}
)

func testFrameID(t *testing.T, nodeID shared.UUID) shared.UUID {
	t.Helper()
	nodeFrameMu.Lock()
	defer nodeFrameMu.Unlock()
	id, ok := nodeFrameMap[nodeID]
	require.True(t, ok, "no frame seeded for node %s — call insertNode first", nodeID)
	return id
}

// withFrameID patches a DispatchRequest to set FrameID = testFrameID(NodeID).
// Tests use this wrapper instead of literal req: enqueueWithFrame(t, q, ctx, req).
func enqueueWithFrame(t *testing.T, q *Queue, ctx context.Context, req queue.DispatchRequest) error {
	t.Helper()
	if req.FrameID == (shared.UUID{}) {
		req.FrameID = testFrameID(t, req.NodeID)
	}
	return q.Enqueue(ctx, req)
}

// insertNode inserts a minimal template → instance → node chain and returns
// the node id. instance_id has a non-nullable FK with ON DELETE CASCADE,
// so we cannot shortcut the chain. state is written verbatim. nodeType is
// rendered into rimsky_nodes.node_type so SelectCandidates' join can return
// it. A running rimsky_frames row is also created for the new instance so
// queue.Enqueue calls can plug `FrameID: testFrameID(t, nodeID)` (the new
// rimsky_dispatch.frame_id NOT NULL constraint per spec §10.2).
func insertNode(t *testing.T, pool *pgxpool.Pool, state, nodeType string) shared.UUID {
	t.Helper()
	nodeID, instID := insertNodeWithInstance(t, pool, state, nodeType)
	frameID := seedFrame(t, pool, instID)
	nodeFrameMu.Lock()
	nodeFrameMap[nodeID] = frameID
	nodeFrameMu.Unlock()
	return nodeID
}

func insertNodeWithInstance(t *testing.T, pool *pgxpool.Pool, state, nodeType string) (nodeID, instanceID shared.UUID) {
	t.Helper()
	ctx := context.Background()

	templateID := uuid.New()
	instanceID = uuid.New()
	nodeID = uuid.New()

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
		`INSERT INTO rimsky_nodes (id, instance_id, node_type, state, dependencies)
		 VALUES ($1, $2, $3, $4, '{}'::uuid[])`,
		nodeID, instanceID, nodeType, state,
	)
	require.NoError(t, err)

	return nodeID, instanceID
}

// seedFrame inserts a running rimsky_frames row for the given instance
// and returns its frame_id. Used to satisfy the rimsky_dispatch.frame_id
// NOT NULL constraint in queue tests.
func seedFrame(t *testing.T, pool *pgxpool.Pool, instanceID shared.UUID) shared.UUID {
	t.Helper()
	ctx := context.Background()
	var id shared.UUID
	require.NoError(t, pool.QueryRow(ctx, `
        INSERT INTO rimsky_frames
            (instance_id, mode, state, source_node_ids, queued_at, started_at, frame_timeout_ms)
        VALUES ($1, 'serial_queue', 'running', ARRAY[gen_random_uuid()]::UUID[], now(), now(), 600000)
        RETURNING frame_id
    `, instanceID).Scan(&id))
	return id
}

func newQueueWithPool(t *testing.T) (*Queue, *pgxpool.Pool, func()) {
	t.Helper()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	return New(pool), pool, teardown
}

// claimWithRunner emulates the runner orchestration the supervisor will
// implement: open tx → SelectCandidates → ClaimDispatchRow → COMMIT, and
// return the claimed candidate (or nil if none). It is a test-only
// shorthand and intentionally skips the §7.3 step 2 lock-eligibility step
// (named/region/claim) that the real runner does in Go between the two
// helpers — these tests don't exercise lock specs.
func claimWithRunner(
	ctx context.Context, t *testing.T, q *Queue,
	supervisorID string, accepts queue.SelectCandidatesRequest,
) *queue.Candidate {
	t.Helper()
	tx, err := q.Pool().BeginTx(ctx, pgx.TxOptions{})
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	candidates, err := q.SelectCandidates(ctx, tx, accepts)
	require.NoError(t, err)
	if len(candidates) == 0 {
		return nil
	}
	cand := candidates[0]
	claimed, err := q.ClaimDispatchRow(ctx, tx, cand.DispatchID, supervisorID)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, tx.Commit(ctx))
	return &cand
}

func TestSelectCandidates_ReturnsNilWhenEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q, _, teardown := newQueueWithPool(t)
	t.Cleanup(teardown)

	tx, err := q.Pool().BeginTx(ctx, pgx.TxOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	cands, err := q.SelectCandidates(ctx, tx, queue.SelectCandidatesRequest{
		AcceptedExecutors: []string{"ingest"},
		AcceptedStores:    []string{},
	})
	require.NoError(t, err)
	require.Empty(t, cands)
}

func TestSelectCandidates_RequiresTx(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q, _, teardown := newQueueWithPool(t)
	t.Cleanup(teardown)

	_, err := q.SelectCandidates(ctx, nil, queue.SelectCandidatesRequest{})
	require.Error(t, err)
}

func TestEnqueueAndClaim_Happy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q, pool, teardown := newQueueWithPool(t)
	t.Cleanup(teardown)

	nodeID := insertNode(t, pool, "stale", "ingest_type")

	require.NoError(t, enqueueWithFrame(t, q, ctx, queue.DispatchRequest{
		NodeID:         nodeID,
		ExecutorName:   "ingest",
		RequiredStores: []string{"items_store"},
		EnqueuedAt:     time.Now().Add(-time.Second),
	}))

	cand := claimWithRunner(ctx, t, q, "sup-1", queue.SelectCandidatesRequest{
		AcceptedExecutors: []string{"ingest"},
		AcceptedStores:    []string{"items_store"},
	})
	require.NotNil(t, cand)
	require.Equal(t, nodeID, cand.NodeID)
	require.Equal(t, "ingest", cand.ExecutorName)
	require.Equal(t, "ingest_type", cand.NodeType)
	require.ElementsMatch(t, []string{"items_store"}, cand.RequiredStores)

	// GetClaimedBy reports "claimed_by" now.
	own, err := q.GetClaimedBy(ctx, cand.DispatchID)
	require.NoError(t, err)
	require.Equal(t, "claimed_by", own.Kind)
	require.Equal(t, "sup-1", own.SupervisorID)

	// last_heartbeat_at was set by ClaimDispatchRow.
	var lastHB *time.Time
	err = pool.QueryRow(ctx,
		`SELECT last_heartbeat_at FROM rimsky_dispatch WHERE id = $1`, cand.DispatchID,
	).Scan(&lastHB)
	require.NoError(t, err)
	require.NotNil(t, lastHB)
}

func TestEnqueue_NativeNodeHasNullExecutor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q, pool, teardown := newQueueWithPool(t)
	t.Cleanup(teardown)

	nodeID := insertNode(t, pool, "stale", "native_type")
	require.NoError(t, enqueueWithFrame(t, q, ctx, queue.DispatchRequest{
		NodeID:     nodeID,
		EnqueuedAt: time.Now().Add(-time.Second),
	}))

	var execName *string
	err := pool.QueryRow(ctx,
		`SELECT executor_name FROM rimsky_dispatch WHERE node_id = $1`, nodeID,
	).Scan(&execName)
	require.NoError(t, err)
	require.Nil(t, execName, "native nodes enqueue with executor_name IS NULL")
}

func TestSelectCandidates_NativeNodesAreReturnedRegardlessOfExecutorAcceptList(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q, pool, teardown := newQueueWithPool(t)
	t.Cleanup(teardown)

	nodeID := insertNode(t, pool, "stale", "native_type")
	require.NoError(t, enqueueWithFrame(t, q, ctx, queue.DispatchRequest{
		NodeID:     nodeID,
		EnqueuedAt: time.Now().Add(-time.Second),
	}))

	cand := claimWithRunner(ctx, t, q, "sup-1", queue.SelectCandidatesRequest{
		AcceptedExecutors: []string{"unrelated"},
		AcceptedStores:    []string{},
	})
	require.NotNil(t, cand, "native (executor IS NULL) candidate must surface even when acceptedExecutors does not list it")
	require.Equal(t, nodeID, cand.NodeID)
	require.Equal(t, "", cand.ExecutorName)
}

func TestSelectCandidates_FiltersByAcceptedExecutors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q, pool, teardown := newQueueWithPool(t)
	t.Cleanup(teardown)

	nodeID := insertNode(t, pool, "stale", "ingest_type")
	require.NoError(t, enqueueWithFrame(t, q, ctx, queue.DispatchRequest{
		NodeID:       nodeID,
		ExecutorName: "ingest",
		EnqueuedAt:   time.Now(),
	}))

	got := claimWithRunner(ctx, t, q, "sup-1", queue.SelectCandidatesRequest{
		AcceptedExecutors: []string{"other"},
		AcceptedStores:    []string{},
	})
	require.Nil(t, got, "executor not in accepts must not surface")

	got = claimWithRunner(ctx, t, q, "sup-1", queue.SelectCandidatesRequest{
		AcceptedExecutors: []string{"ingest"},
		AcceptedStores:    []string{},
	})
	require.NotNil(t, got)
}

func TestSelectCandidates_FiltersByRequiredStores(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q, pool, teardown := newQueueWithPool(t)
	t.Cleanup(teardown)

	// Two enqueued nodes: one needs store A, the other needs store B.
	nA := insertNode(t, pool, "stale", "type_a")
	nB := insertNode(t, pool, "stale", "type_b")

	require.NoError(t, enqueueWithFrame(t, q, ctx, queue.DispatchRequest{
		NodeID:         nA,
		ExecutorName:   "ingest",
		RequiredStores: []string{"store_a"},
		EnqueuedAt:     time.Now().Add(-time.Second),
	}))
	require.NoError(t, enqueueWithFrame(t, q, ctx, queue.DispatchRequest{
		NodeID:         nB,
		ExecutorName:   "ingest",
		RequiredStores: []string{"store_b"},
		EnqueuedAt:     time.Now().Add(1 * time.Millisecond),
	}))

	// A supervisor with only store_a may only pick up nA.
	tx, err := q.Pool().BeginTx(ctx, pgx.TxOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	cands, err := q.SelectCandidates(ctx, tx, queue.SelectCandidatesRequest{
		AcceptedExecutors: []string{"ingest"},
		AcceptedStores:    []string{"store_a"},
	})
	require.NoError(t, err)
	require.Len(t, cands, 1)
	require.Equal(t, nA, cands[0].NodeID)
}

func TestSelectCandidates_RespectsEnqueuedAt_InFuture(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q, pool, teardown := newQueueWithPool(t)
	t.Cleanup(teardown)

	nodeID := insertNode(t, pool, "stale", "ingest_type")
	require.NoError(t, enqueueWithFrame(t, q, ctx, queue.DispatchRequest{
		NodeID:       nodeID,
		ExecutorName: "ingest",
		EnqueuedAt:   time.Now().Add(1 * time.Hour),
	}))

	got := claimWithRunner(ctx, t, q, "sup-1", queue.SelectCandidatesRequest{
		AcceptedExecutors: []string{"ingest"},
	})
	require.Nil(t, got, "future-dated enqueue must not be claimable yet")
}

func TestSelectCandidates_SkipLocked_TwoConcurrentTransactions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q, pool, teardown := newQueueWithPool(t)
	t.Cleanup(teardown)

	nodeID := insertNode(t, pool, "stale", "ingest_type")
	require.NoError(t, enqueueWithFrame(t, q, ctx, queue.DispatchRequest{
		NodeID: nodeID, ExecutorName: "ingest", EnqueuedAt: time.Now().Add(-time.Second),
	}))

	// Open two transactions in parallel; SKIP LOCKED must give the row
	// to exactly one. The runner pattern: tx → SelectCandidates →
	// ClaimDispatchRow → COMMIT.
	var wg sync.WaitGroup
	results := make([]bool, 2)
	errs := make([]error, 2)
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func(idx int) {
			defer wg.Done()
			tx, err := q.Pool().BeginTx(ctx, pgx.TxOptions{})
			if err != nil {
				errs[idx] = err
				return
			}
			defer func() { _ = tx.Rollback(ctx) }()

			cands, err := q.SelectCandidates(ctx, tx, queue.SelectCandidatesRequest{
				AcceptedExecutors: []string{"ingest"},
			})
			if err != nil {
				errs[idx] = err
				return
			}
			if len(cands) == 0 {
				return
			}
			ok, err := q.ClaimDispatchRow(ctx, tx, cands[0].DispatchID, "sup-x")
			if err != nil {
				errs[idx] = err
				return
			}
			if ok {
				if err := tx.Commit(ctx); err != nil {
					errs[idx] = err
					return
				}
				results[idx] = true
			}
		}(i)
	}
	wg.Wait()

	require.NoError(t, errs[0])
	require.NoError(t, errs[1])
	winners := 0
	for _, w := range results {
		if w {
			winners++
		}
	}
	require.Equal(t, 1, winners, "exactly one tx may win the dispatch row under SKIP LOCKED")

	// Verify state at rest.
	var claimedBy *string
	err := pool.QueryRow(ctx,
		`SELECT claimed_by FROM rimsky_dispatch WHERE node_id = $1`, nodeID,
	).Scan(&claimedBy)
	require.NoError(t, err)
	require.NotNil(t, claimedBy)
}

func TestClaimDispatchRow_GuardedReturnsFalseWhenAlreadyClaimed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q, pool, teardown := newQueueWithPool(t)
	t.Cleanup(teardown)

	nodeID := insertNode(t, pool, "stale", "ingest_type")
	require.NoError(t, enqueueWithFrame(t, q, ctx, queue.DispatchRequest{
		NodeID: nodeID, ExecutorName: "ingest", EnqueuedAt: time.Now().Add(-time.Second),
	}))

	// First claim through the runner pattern.
	cand := claimWithRunner(ctx, t, q, "sup-A", queue.SelectCandidatesRequest{
		AcceptedExecutors: []string{"ingest"},
	})
	require.NotNil(t, cand)

	// Second attempt to claim the same dispatch id from a different supervisor:
	// the row is no longer claimed_by IS NULL, so the guarded UPDATE returns
	// claimed=false (defensive guard per spec §7.3 step 3c).
	tx, err := q.Pool().BeginTx(ctx, pgx.TxOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	claimed, err := q.ClaimDispatchRow(ctx, tx, cand.DispatchID, "sup-B")
	require.NoError(t, err)
	require.False(t, claimed)
	require.NoError(t, tx.Commit(ctx))

	// Ownership unchanged.
	own, err := q.GetClaimedBy(ctx, cand.DispatchID)
	require.NoError(t, err)
	require.Equal(t, "sup-A", own.SupervisorID)
	_ = pool
}

func TestTakeNamedLockAdvisory_SerializesConcurrentHolders(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q, _, teardown := newQueueWithPool(t)
	t.Cleanup(teardown)

	// Two transactions racing for the same advisory lock; the second must
	// block until the first commits/rolls back. We assert serialization by
	// observing that the second tx cannot acquire while the first sleeps.
	tx1, err := q.Pool().BeginTx(ctx, pgx.TxOptions{})
	require.NoError(t, err)
	defer func() { _ = tx1.Rollback(ctx) }()

	require.NoError(t, TakeNamedLockAdvisory(ctx, tx1, "lock_one"))

	// In a goroutine, take the same advisory in tx2 — should block.
	acquired := make(chan struct{})
	tx2started := make(chan struct{})
	go func() {
		tx2, err := q.Pool().BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return
		}
		defer func() { _ = tx2.Rollback(ctx) }()
		close(tx2started)
		if err := TakeNamedLockAdvisory(ctx, tx2, "lock_one"); err != nil {
			return
		}
		close(acquired)
	}()

	<-tx2started
	select {
	case <-acquired:
		t.Fatal("tx2 acquired advisory while tx1 still holds it — should be blocked")
	case <-time.After(200 * time.Millisecond):
		// expected: still blocked
	}

	// Release tx1; tx2 should acquire shortly after.
	require.NoError(t, tx1.Rollback(ctx))
	select {
	case <-acquired:
		// good
	case <-time.After(2 * time.Second):
		t.Fatal("tx2 never acquired advisory after tx1 released")
	}
}

func TestTakeNamedLockAdvisory_RequiresTx(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	require.Error(t, TakeNamedLockAdvisory(ctx, nil, "x"))
}

// TestTakeRegionAdvisory_SerializesConcurrentHolders is the regression
// cover for the v3 cycle-4 fix that introduced TakeRegionAdvisory. Two
// supervisors evaluating region-conflict against each other's
// uncommitted INSERTs (READ COMMITTED hides them) would both pass the
// in-Go conflict predicate without serialization — both would commit,
// violating single-writer-per-region (blessed-invariant 4b). The
// per-(store_name, region_data) advisory lock prevents this. This test
// asserts that two transactions targeting the same (store, region) pair
// serialize as expected, while distinct keys do NOT.
func TestTakeRegionAdvisory_SerializesConcurrentHolders(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q, _, teardown := newQueueWithPool(t)
	t.Cleanup(teardown)

	storeName := "content"
	regionA := []byte("/region-A")

	tx1, err := q.Pool().BeginTx(ctx, pgx.TxOptions{})
	require.NoError(t, err)
	defer func() { _ = tx1.Rollback(ctx) }()
	require.NoError(t, TakeRegionAdvisory(ctx, tx1, storeName, regionA))

	// Same key — must block until tx1 releases.
	acquired := make(chan struct{})
	tx2started := make(chan struct{})
	go func() {
		tx2, err := q.Pool().BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return
		}
		defer func() { _ = tx2.Rollback(ctx) }()
		close(tx2started)
		if err := TakeRegionAdvisory(ctx, tx2, storeName, regionA); err != nil {
			return
		}
		close(acquired)
	}()

	<-tx2started
	select {
	case <-acquired:
		t.Fatal("tx2 acquired region advisory while tx1 still holds it — should be blocked")
	case <-time.After(200 * time.Millisecond):
		// expected: still blocked
	}

	require.NoError(t, tx1.Rollback(ctx))
	select {
	case <-acquired:
		// good
	case <-time.After(2 * time.Second):
		t.Fatal("tx2 never acquired region advisory after tx1 released")
	}
}

// TestTakeRegionAdvisory_DistinctKeysDoNotBlock asserts that two
// transactions on different (store, region) pairs proceed in parallel —
// the advisory key is keyed by both fields, so disjoint regions on the
// same store, or the same region on different stores, are not serialized.
func TestTakeRegionAdvisory_DistinctKeysDoNotBlock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q, _, teardown := newQueueWithPool(t)
	t.Cleanup(teardown)

	tx1, err := q.Pool().BeginTx(ctx, pgx.TxOptions{})
	require.NoError(t, err)
	defer func() { _ = tx1.Rollback(ctx) }()
	require.NoError(t, TakeRegionAdvisory(ctx, tx1, "store-A", []byte("/region-A")))

	tx2, err := q.Pool().BeginTx(ctx, pgx.TxOptions{})
	require.NoError(t, err)
	defer func() { _ = tx2.Rollback(ctx) }()
	// Distinct region on the same store — must NOT block.
	require.NoError(t, TakeRegionAdvisory(ctx, tx2, "store-A", []byte("/region-B")))

	tx3, err := q.Pool().BeginTx(ctx, pgx.TxOptions{})
	require.NoError(t, err)
	defer func() { _ = tx3.Rollback(ctx) }()
	// Same region on a different store — must NOT block.
	require.NoError(t, TakeRegionAdvisory(ctx, tx3, "store-B", []byte("/region-A")))
}

func TestTakeRegionAdvisory_RequiresTx(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	require.Error(t, TakeRegionAdvisory(ctx, nil, "store-A", []byte("/region-A")))
}

func TestReleaseClaim_Guarded_NoOpOnMismatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q, pool, teardown := newQueueWithPool(t)
	t.Cleanup(teardown)

	nodeID := insertNode(t, pool, "stale", "ingest_type")
	require.NoError(t, enqueueWithFrame(t, q, ctx, queue.DispatchRequest{
		NodeID: nodeID, ExecutorName: "ingest", EnqueuedAt: time.Now().Add(-time.Second),
	}))
	cand := claimWithRunner(ctx, t, q, "sup-winner", queue.SelectCandidatesRequest{
		AcceptedExecutors: []string{"ingest"},
	})
	require.NotNil(t, cand)

	// Guarded release by a DIFFERENT supervisor must be a no-op.
	require.NoError(t, q.ReleaseClaim(ctx, cand.DispatchID, "sup-ghost"))

	own, err := q.GetClaimedBy(ctx, cand.DispatchID)
	require.NoError(t, err)
	require.Equal(t, "claimed_by", own.Kind)
	require.Equal(t, "sup-winner", own.SupervisorID)

	// Guarded release by the correct supervisor clears claim and
	// last_heartbeat_at.
	require.NoError(t, q.ReleaseClaim(ctx, cand.DispatchID, "sup-winner"))
	own, err = q.GetClaimedBy(ctx, cand.DispatchID)
	require.NoError(t, err)
	require.Equal(t, "unclaimed", own.Kind)

	var lastHB *time.Time
	err = pool.QueryRow(ctx,
		`SELECT last_heartbeat_at FROM rimsky_dispatch WHERE id = $1`, cand.DispatchID,
	).Scan(&lastHB)
	require.NoError(t, err)
	require.Nil(t, lastHB, "ReleaseClaim must null last_heartbeat_at alongside the claim columns")
}

func TestListOrphanedClaims_FiltersOnLastHeartbeatAt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q, pool, teardown := newQueueWithPool(t)
	t.Cleanup(teardown)

	// Two claimed rows; one's last_heartbeat_at gets backdated past the
	// cutoff, the other stays fresh.
	nStale := insertNode(t, pool, "stale", "type_x")
	nFresh := insertNode(t, pool, "stale", "type_x")

	require.NoError(t, enqueueWithFrame(t, q, ctx, queue.DispatchRequest{
		NodeID: nStale, ExecutorName: "ingest", EnqueuedAt: time.Now().Add(-time.Second),
	}))
	require.NoError(t, enqueueWithFrame(t, q, ctx, queue.DispatchRequest{
		NodeID: nFresh, ExecutorName: "ingest", EnqueuedAt: time.Now().Add(1 * time.Millisecond),
	}))

	c1 := claimWithRunner(ctx, t, q, "sup-1", queue.SelectCandidatesRequest{AcceptedExecutors: []string{"ingest"}})
	require.NotNil(t, c1)
	c2 := claimWithRunner(ctx, t, q, "sup-1", queue.SelectCandidatesRequest{AcceptedExecutors: []string{"ingest"}})
	require.NotNil(t, c2)

	// Backdate just the first row's last_heartbeat_at.
	_, err := pool.Exec(ctx,
		`UPDATE rimsky_dispatch
		    SET last_heartbeat_at = NOW() - INTERVAL '10 minutes'
		  WHERE node_id = $1`,
		nStale,
	)
	require.NoError(t, err)

	orphans, err := q.ListOrphanedClaims(ctx, time.Now().Add(-1*time.Minute))
	require.NoError(t, err)
	require.Len(t, orphans, 1)
	require.Equal(t, nStale, orphans[0].NodeID)
	require.NotNil(t, orphans[0].LastHeartbeatAt)
}

func TestRefreshHeartbeat_BumpsClaimedRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q, pool, teardown := newQueueWithPool(t)
	t.Cleanup(teardown)

	// One claimed row whose last_heartbeat_at is backdated; RefreshHeartbeat
	// should bring it forward. A row claimed by a different supervisor must
	// not be touched.
	n1 := insertNode(t, pool, "stale", "type_x")
	n2 := insertNode(t, pool, "stale", "type_x")

	require.NoError(t, enqueueWithFrame(t, q, ctx, queue.DispatchRequest{
		NodeID: n1, ExecutorName: "ingest", EnqueuedAt: time.Now().Add(-time.Second),
	}))
	require.NoError(t, enqueueWithFrame(t, q, ctx, queue.DispatchRequest{
		NodeID: n2, ExecutorName: "ingest", EnqueuedAt: time.Now().Add(1 * time.Millisecond),
	}))

	c1 := claimWithRunner(ctx, t, q, "sup-mine", queue.SelectCandidatesRequest{AcceptedExecutors: []string{"ingest"}})
	require.NotNil(t, c1)
	c2 := claimWithRunner(ctx, t, q, "sup-other", queue.SelectCandidatesRequest{AcceptedExecutors: []string{"ingest"}})
	require.NotNil(t, c2)

	// Backdate both heartbeats.
	_, err := pool.Exec(ctx,
		`UPDATE rimsky_dispatch SET last_heartbeat_at = NOW() - INTERVAL '10 minutes'`,
	)
	require.NoError(t, err)

	require.NoError(t, q.RefreshHeartbeat(ctx, "sup-mine"))

	// Mine bumped to ~now; other unchanged.
	var hbMine, hbOther *time.Time
	err = pool.QueryRow(ctx,
		`SELECT last_heartbeat_at FROM rimsky_dispatch WHERE id = $1`, c1.DispatchID,
	).Scan(&hbMine)
	require.NoError(t, err)
	err = pool.QueryRow(ctx,
		`SELECT last_heartbeat_at FROM rimsky_dispatch WHERE id = $1`, c2.DispatchID,
	).Scan(&hbOther)
	require.NoError(t, err)

	require.NotNil(t, hbMine)
	require.NotNil(t, hbOther)
	require.WithinDuration(t, time.Now(), *hbMine, 5*time.Second, "mine should be bumped")
	require.True(t, time.Since(*hbOther) > 1*time.Minute, "other must not have moved")
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
	nodeID := insertNode(t, pool, "stale", "ingest_type")
	require.NoError(t, enqueueWithFrame(t, q, ctx, queue.DispatchRequest{
		NodeID: nodeID, ExecutorName: "ingest", EnqueuedAt: time.Now().Add(-time.Second),
	}))
	var dispatchID shared.UUID
	err = pool.QueryRow(ctx, `SELECT id FROM rimsky_dispatch WHERE node_id = $1`, nodeID).Scan(&dispatchID)
	require.NoError(t, err)

	own, err = q.GetClaimedBy(ctx, dispatchID)
	require.NoError(t, err)
	require.Equal(t, "unclaimed", own.Kind)

	// claimed_by
	cand := claimWithRunner(ctx, t, q, "sup-xyz", queue.SelectCandidatesRequest{AcceptedExecutors: []string{"ingest"}})
	require.NotNil(t, cand)

	own, err = q.GetClaimedBy(ctx, cand.DispatchID)
	require.NoError(t, err)
	require.Equal(t, "claimed_by", own.Kind)
	require.Equal(t, "sup-xyz", own.SupervisorID)
}

func TestComplete_GuardedDoesNotDeleteFreshClaim(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q, pool, teardown := newQueueWithPool(t)
	t.Cleanup(teardown)

	nodeID := insertNode(t, pool, "stale", "ingest_type")
	require.NoError(t, enqueueWithFrame(t, q, ctx, queue.DispatchRequest{
		NodeID: nodeID, ExecutorName: "ingest", EnqueuedAt: time.Now().Add(-time.Second),
	}))
	cand := claimWithRunner(ctx, t, q, "sup-fresh", queue.SelectCandidatesRequest{AcceptedExecutors: []string{"ingest"}})
	require.NotNil(t, cand)

	// A stale supervisor's guarded Complete must NOT delete the row.
	require.NoError(t, q.Complete(ctx, cand.DispatchID, "sup-stale"))

	own, err := q.GetClaimedBy(ctx, cand.DispatchID)
	require.NoError(t, err)
	require.Equal(t, "claimed_by", own.Kind)
	require.Equal(t, "sup-fresh", own.SupervisorID)

	// The rightful owner's guarded Complete deletes it.
	require.NoError(t, q.Complete(ctx, cand.DispatchID, "sup-fresh"))
	own, err = q.GetClaimedBy(ctx, cand.DispatchID)
	require.NoError(t, err)
	require.Equal(t, "not_found", own.Kind)

	_ = pool
}

func TestRemoveForNode_GuardedDoesNotRemoveLiveClaim(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q, pool, teardown := newQueueWithPool(t)
	t.Cleanup(teardown)

	nodeID := insertNode(t, pool, "stale", "ingest_type")
	// EnqueuedAt deliberately in the past so the
	// `enqueued_at <= NOW()` predicate isn't flaky against the
	// postgres container's clock.
	require.NoError(t, enqueueWithFrame(t, q, ctx, queue.DispatchRequest{
		NodeID: nodeID, ExecutorName: "ingest", EnqueuedAt: time.Now().Add(-time.Second),
	}))
	cand := claimWithRunner(ctx, t, q, "sup-live", queue.SelectCandidatesRequest{AcceptedExecutors: []string{"ingest"}})
	require.NotNil(t, cand)

	// Wrong-supervisor guarded RemoveForNode is a no-op.
	require.NoError(t, q.RemoveForNode(ctx, nodeID, "sup-other"))
	var stillThere int
	err := pool.QueryRow(ctx, `SELECT count(*) FROM rimsky_dispatch WHERE node_id = $1`, nodeID).Scan(&stillThere)
	require.NoError(t, err)
	require.Equal(t, 1, stillThere)

	// Right-supervisor removes the row.
	require.NoError(t, q.RemoveForNode(ctx, nodeID, "sup-live"))
	err = pool.QueryRow(ctx, `SELECT count(*) FROM rimsky_dispatch WHERE node_id = $1`, nodeID).Scan(&stillThere)
	require.NoError(t, err)
	require.Equal(t, 0, stillThere)
}
