// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	nodepkg "github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/test/support/pgdbtest"
)

type fakeQueue struct {
	mu       sync.Mutex
	enqueued []persistence.DispatchRequest
}

func (f *fakeQueue) Enqueue(_ context.Context, req persistence.DispatchRequest, _ persistence.Tx) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enqueued = append(f.enqueued, req)
	return nil
}
func (f *fakeQueue) SelectCandidates(_ context.Context, _ persistence.SelectCandidatesRequest, _ persistence.Tx) ([]persistence.Candidate, error) {
	return nil, nil
}
func (f *fakeQueue) ClaimDispatchRow(_ context.Context, _ shared.UUID, _ string, _ persistence.Tx) (bool, error) {
	return false, nil
}
func (f *fakeQueue) PromoteClaimedToRunning(_ context.Context, _ shared.UUID, _ string, _ persistence.Tx) (bool, error) {
	return false, nil
}
func (f *fakeQueue) Complete(_ context.Context, _ shared.UUID, _ string) error { return nil }
func (f *fakeQueue) ForceComplete(_ context.Context, _ shared.UUID) error      { return nil }
func (f *fakeQueue) RemoveForNode(_ context.Context, _ shared.UUID, _ shared.UUID, _ string, _ persistence.Tx) error {
	return nil
}
func (f *fakeQueue) ForceRemoveForNode(_ context.Context, _ shared.UUID, _ shared.UUID, _ persistence.Tx) error {
	return nil
}
func (f *fakeQueue) ListOrphanedClaims(_ context.Context) ([]persistence.DispatchRow, error) {
	return nil, nil
}
func (f *fakeQueue) ReleaseClaim(_ context.Context, _ shared.UUID, _ string) error { return nil }
func (f *fakeQueue) ReleaseClaimWithDisposition(_ context.Context, _ shared.UUID, _ string, _ string) error {
	return nil
}
func (f *fakeQueue) StampPriorDispatch(_ context.Context, _ shared.UUID, _ shared.UUID, _ string, _ persistence.Tx) error {
	return nil
}

func (f *fakeQueue) GetClaimedBy(_ context.Context, _ shared.UUID) (persistence.ClaimOwnership, error) {
	return persistence.ClaimOwnership{Kind: "not_found"}, nil
}
func (f *fakeQueue) GetDispatchNode(_ context.Context, _ shared.UUID, _ persistence.Tx) (shared.UUID, persistence.ClaimOwnership, error) {
	return shared.UUID{}, persistence.ClaimOwnership{Kind: "not_found"}, nil
}
func (f *fakeQueue) RefreshHeartbeat(_ context.Context, _ string) error { return nil }
func (f *fakeQueue) ListLive(_ context.Context, _ persistence.DispatchListFilter, _ persistence.ListPagination) (persistence.PaginatedListResult[persistence.DispatchRow], error) {
	return persistence.PaginatedListResult[persistence.DispatchRow]{}, nil
}
func (f *fakeQueue) CountLive(_ context.Context, _ persistence.DispatchListFilter) (int, error) {
	return 0, nil
}
func (f *fakeQueue) GetByID(_ context.Context, _ shared.UUID) (*persistence.DispatchRow, error) {
	return nil, nil
}

func (f *fakeQueue) GetAnyByID(_ context.Context, _ shared.UUID) (*persistence.DispatchRow, error) {
	return nil, nil
}
func (f *fakeQueue) GetInFlightRunForNode(_ context.Context, _ shared.UUID, _ shared.UUID, _ persistence.Tx) (shared.UUID, bool, error) {
	return shared.UUID{}, false, nil
}
func (f *fakeQueue) GetMostRecentRunForNodeInScope(_ context.Context, _ shared.UUID, _ shared.UUID, _ persistence.Tx) (shared.UUID, bool, error) {
	return shared.UUID{}, false, nil
}
func (f *fakeQueue) ListInFlightRunStates(context.Context, []shared.UUID, shared.UUID, shared.UUID, persistence.Tx) (map[shared.UUID][]string, error) {
	return map[shared.UUID][]string{}, nil
}

func (f *fakeQueue) ParkActive(_ context.Context, _ persistence.ParkActiveInput, _ persistence.Tx) error {
	return nil
}
func (f *fakeQueue) ListParkedReadyForResume(_ context.Context, _ time.Time, _ int) ([]persistence.ParkedRow, error) {
	return nil, nil
}
func (f *fakeQueue) GetParkedByNode(_ context.Context, _ shared.UUID, _ shared.UUID, _ persistence.Tx) (*persistence.ParkedRow, error) {
	return nil, nil
}
func (f *fakeQueue) ResumeParked(_ context.Context, _ shared.UUID, _ persistence.Tx) (bool, error) {
	return false, nil
}
func (f *fakeQueue) BumpLastProgressAt(_ context.Context, _ shared.UUID, _ time.Time, _ persistence.Tx) (bool, error) {
	return true, nil
}
func (f *fakeQueue) RegisterAsyncAck(_ context.Context, _ shared.UUID, _ string, _ time.Time, _ *int, _ *int, _ string, _ string, _ persistence.Tx) error {
	return nil
}
func (f *fakeQueue) LookupRunByAsyncAckID(_ context.Context, _ string, _ persistence.Tx) (*persistence.DispatchRow, error) {
	return nil, nil
}
func (f *fakeQueue) LoadScratch(_ context.Context, _ shared.UUID, _ persistence.Tx) ([]byte, string, string, error) {
	return nil, "", "", nil
}
func (f *fakeQueue) WriteScratch(_ context.Context, _ shared.UUID, _ []byte, _, _ string, _ persistence.Tx) error {
	return nil
}

func (f *fakeQueue) CountParked(_ context.Context) (int, error) {
	return 0, nil
}

func (f *fakeQueue) ListParkedDiagnostic(_ context.Context, _ persistence.Tx) ([]persistence.ParkedDiagnosticRow, error) {
	return nil, nil
}

func (f *fakeQueue) snapshot() []persistence.DispatchRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]persistence.DispatchRequest, len(f.enqueued))
	copy(out, f.enqueued)
	return out
}

var _ persistence.Queue = (*fakeQueue)(nil)

type pcFixture struct {
	persist persistence.Tables
	driver  persistence.Database
}

func newPureCascadeFixture(t *testing.T) *pcFixture {
	t.Helper()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)
	return &pcFixture{persist: d.Tables(), driver: d}
}

func pcDeployTemplate(ctx context.Context, t *testing.T, b persistence.Tables, name string) persistence.TemplateRow {
	t.Helper()
	return insertDeployedTemplate(ctx, t, b, nodepkg.TemplateSpec{
		Name: name, Version: "v1", Description: "test",
		Nodes: []nodepkg.TemplateNodeDef{},
	})
}

func pcCreateInstance(ctx context.Context, t *testing.T, b persistence.Tables, templateHash string, ck string) persistence.InstanceRow {
	t.Helper()
	ckCopy := ck
	var inst persistence.InstanceRow
	instID := shared.UUID(uuid.New())
	mainScopeID := shared.UUID(uuid.New())
	inTxTest(t, ctx, b, func(tx persistence.Tx) error {
		if err := b.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID:         mainScopeID,
			GraphName:  "main",
			InstanceID: instID,
		}, tx); err != nil {
			return err
		}
		row, err := b.Instances().Create(ctx, persistence.InstanceCreateInput{TargetRoutingIdentity: "test-agent",
			ID: instID, TemplateHash: templateHash, InstanceKey: &ckCopy, Params: map[string]any{},
		}, tx)
		if err != nil {
			return err
		}
		inst = row
		return nil
	})
	return inst
}

func pcCreateNode(ctx context.Context, t *testing.T, f *pcFixture, instanceID shared.UUID, executor string, deps ...shared.UUID) persistence.NodeRow {
	t.Helper()
	_ = deps
	return pcCreateNodeWithType(ctx, t, f, instanceID, "t", executor)
}

func pcCreateNodeWithType(ctx context.Context, t *testing.T, f *pcFixture, instanceID shared.UUID, nodeType, executor string) persistence.NodeRow {
	t.Helper()
	var n persistence.NodeRow
	inTxTest(t, ctx, f.persist, func(tx persistence.Tx) error {
		row, err := f.persist.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: uuid.New(), InstanceID: instanceID, NodeType: nodeType,
			Executor: executor,
		}, tx)
		if err != nil {
			return err
		}
		n = row
		return nil
	})
	forceState(ctx, t, f, n.ID, "stale")
	return n
}

func forceState(ctx context.Context, t *testing.T, f *pcFixture, id shared.UUID, state string) {
	t.Helper()
	if state == "fresh" {
		pgdbtest.ExecForTest(ctx, t, f.driver,
			`DELETE FROM rimsky_node_runs WHERE node_id = $1
			    AND state IN ('pending','stale','running','held','parked')`, id)
		return
	}
	var (
		executorN  sql.NullString
		instanceID shared.UUID
		frameN     sql.NullString
	)
	pgdbtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT executor, instance_id::text FROM rimsky_nodes WHERE id = $1`,
		[]any{id}, &executorN, &instanceID)
	{
		var count int
		pgdbtest.QueryRowForTest(ctx, t, f.driver,
			`SELECT COUNT(*) FROM rimsky_frames WHERE instance_id = $1 AND ended_at IS NULL`,
			[]any{instanceID}, &count)
		var fid shared.UUID
		if count == 0 {
			fid = pcSeedFrame(ctx, t, f, instanceID, id)
		} else {
			pgdbtest.QueryRowForTest(ctx, t, f.driver,
				`SELECT frame_id FROM rimsky_frames WHERE instance_id = $1 AND ended_at IS NULL LIMIT 1`,
				[]any{instanceID}, &fid)
		}
		frameN = sql.NullString{String: fid.String(), Valid: true}
	}
	var mainScopeID shared.UUID
	pgdbtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT id FROM rimsky_run_scopes WHERE instance_id = $1 AND graph_name = 'main'`,
		[]any{instanceID}, &mainScopeID)
	pgdbtest.ExecForTest(ctx, t, f.driver,
		`INSERT INTO rimsky_node_runs (id, node_id, executor_name, required_claim_producers,
		                               enqueued_at, state, sequence, creation_reason, frame_id, run_scope_id)
		 VALUES (gen_random_uuid(), $1, $2, '{}', NOW(), $3, 1, 'cascade', $4::uuid, $5)`,
		id, executorN.String, state, frameN.String, mainScopeID)
}

func pcSeedFrame(ctx context.Context, t *testing.T, f *pcFixture, instanceID, nodeID shared.UUID) shared.UUID {
	t.Helper()
	var count int
	pgdbtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT COUNT(*) FROM rimsky_frames WHERE instance_id = $1 AND ended_at IS NULL`,
		[]any{instanceID}, &count)
	var frameID shared.UUID
	if count == 0 {
		var rootScope shared.UUID
		pgdbtest.QueryRowForTest(ctx, t, f.driver,
			`SELECT id FROM rimsky_run_scopes WHERE instance_id = $1 AND graph_name = 'main'`,
			[]any{instanceID}, &rootScope)
		msgID := uuid.New()
		pgdbtest.ExecForTest(ctx, t, f.driver, `
            INSERT INTO rimsky_messages
                (id, instance_id, type, sender, sender_kind, received_at)
            VALUES ($1, $2, 'test/seed', 'test', 'operator', now())
        `, msgID, instanceID)
		pgdbtest.QueryRowForTest(ctx, t, f.driver, `
            INSERT INTO rimsky_frames
                (instance_id, triggering_message_id, root_run_scope_id, started_at)
            VALUES ($1, $2, $3, now())
            RETURNING frame_id
        `, []any{instanceID, msgID, rootScope}, &frameID)
		_ = nodeID
	} else {
		pgdbtest.QueryRowForTest(ctx, t, f.driver,
			`SELECT frame_id FROM rimsky_frames WHERE instance_id = $1 AND ended_at IS NULL LIMIT 1`,
			[]any{instanceID}, &frameID)
	}
	_ = nodeID
	return frameID
}

func pcArgs(b persistence.Tables, q *fakeQueue) PureCascadeArgs {
	return PureCascadeArgs{
		Persist: b, Queue: q, Clock: shared.SystemClock{}, Logger: shared.SilentLogger{},
	}
}

func TestProcessPureCascade_NoReady_ReturnsZero(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newPureCascadeFixture(t)

	tpl := pcDeployTemplate(ctx, t, f.persist, "empty")
	_ = pcCreateInstance(ctx, t, f.persist, tpl.ID, "ck-0")

	q := &fakeQueue{}
	count, err := ProcessPureCascade(ctx, pcArgs(f.persist, q))
	require.NoError(t, err)
	assert.Equal(t, 0, count)
	assert.Empty(t, q.snapshot())
}

func TestProcessPureCascade_SingleReady_TransitionsToFreshAndLogsCommit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newPureCascadeFixture(t)

	tpl := pcDeployTemplate(ctx, t, f.persist, "alpha")
	inst := pcCreateInstance(ctx, t, f.persist, tpl.ID, "ck-1")
	pure := pcCreateNode(ctx, t, f, inst.ID, "")

	q := &fakeQueue{}
	count, err := ProcessPureCascade(ctx, pcArgs(f.persist, q))
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	var latest *persistence.NodeRunLatest
	inTxTest(t, ctx, f.persist, func(tx persistence.Tx) error {
		r, err := f.persist.Nodes().GetLatestRunForNode(ctx, pure.ID, tx)
		latest = r
		return err
	})
	require.NotNil(t, latest)
	assert.Equal(t, cascade.NodeStateFresh, latest.State)

	var evs persistence.EventListResult
	inTxTest(t, ctx, f.persist, func(tx persistence.Tx) error {
		r, err := f.persist.Events().List(ctx, persistence.EventListFilter{
			NodeID: &pure.ID, KindIn: []string{"terminal/success"},
		}, persistence.ListPagination{Limit: 100}, tx)
		evs = r
		return err
	})
	require.Len(t, evs.Events, 1)
	require.NotNil(t, evs.Events[0].NodeID)
	assert.Equal(t, pure.ID, *evs.Events[0].NodeID)
	require.NotNil(t, evs.Events[0].InstanceID)
	assert.Equal(t, inst.ID, *evs.Events[0].InstanceID)

	assert.Empty(t, q.snapshot())
}

func TestProcessPureCascade_SingleReady_FiresAfterTerminalBreakpoint(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newPureCascadeFixture(t)

	tpl := pcDeployTemplate(ctx, t, f.persist, "alpha")
	inst := pcCreateInstance(ctx, t, f.persist, tpl.ID, "ck-bp")
	pure := pcCreateNode(ctx, t, f, inst.ID, "")

	var bpID shared.UUID
	inTxTest(t, ctx, f.persist, func(tx persistence.Tx) error {
		id, err := f.persist.Breakpoints().Create(ctx, persistence.BreakpointRow{
			InstanceID:     inst.ID,
			Matcher:        map[string]any{"node_type": "t"},
			Checkpoint:     persistence.CheckpointAfterTerminal,
			Mode:           persistence.BreakpointModeNotifyOnly,
			OverflowPolicy: persistence.OverflowDropOldest,
			HitTTLSeconds:  300,
		}, tx)
		bpID = id
		return err
	})

	q := &fakeQueue{}
	count, err := ProcessPureCascade(ctx, pcArgs(f.persist, q))
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	var hits []persistence.BreakpointHitRow
	inTxTest(t, ctx, f.persist, func(tx persistence.Tx) error {
		r, err := f.persist.BreakpointHits().ListSinceForBreakpoint(ctx, bpID, 0, 100, tx)
		hits = r
		return err
	})
	require.Len(t, hits, 1,
		"concept:breakpoint says executor-less dispatches are observed at the post-terminal checkpoint only; "+
			"a pure-cascade transition settled natively by the scheduler must still fire after_terminal")
	require.NotNil(t, hits[0].NodeRunID)
	assert.Equal(t, persistence.CheckpointAfterTerminal, hits[0].Checkpoint)

	var latest *persistence.NodeRunLatest
	inTxTest(t, ctx, f.persist, func(tx persistence.Tx) error {
		r, err := f.persist.Nodes().GetLatestRunForNode(ctx, pure.ID, tx)
		latest = r
		return err
	})
	require.NotNil(t, latest)
	assert.Equal(t, latest.NodeRunID, *hits[0].NodeRunID,
		"the hit must reference the pure-cascade node run that actually settled, not an unrelated dispatch")
}

func TestProcessPureCascade_WithExecutorNodeIsSkipped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newPureCascadeFixture(t)

	tpl := pcDeployTemplate(ctx, t, f.persist, "alpha")
	inst := pcCreateInstance(ctx, t, f.persist, tpl.ID, "ck-1")
	execNode := pcCreateNode(ctx, t, f, inst.ID, "worker")

	q := &fakeQueue{}
	count, err := ProcessPureCascade(ctx, pcArgs(f.persist, q))
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	var latest *persistence.NodeRunLatest
	inTxTest(t, ctx, f.persist, func(tx persistence.Tx) error {
		r, err := f.persist.Nodes().GetLatestRunForNode(ctx, execNode.ID, tx)
		latest = r
		return err
	})
	require.NotNil(t, latest)
	assert.Equal(t, cascade.NodeStateStale, latest.State)

	var evs persistence.EventListResult
	inTxTest(t, ctx, f.persist, func(tx persistence.Tx) error {
		r, err := f.persist.Events().List(ctx, persistence.EventListFilter{
			NodeID: &execNode.ID, KindIn: []string{"terminal/success"},
		}, persistence.ListPagination{Limit: 100}, tx)
		evs = r
		return err
	})
	assert.Empty(t, evs.Events)

	assert.Empty(t, q.snapshot())
}

func TestProcessPureCascade_NativeClaimOnly_ReusesStaleRunAcrossTicks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newPureCascadeFixture(t)

	sum := insertDeployedTemplate(ctx, t, f.persist, nodepkg.TemplateSpec{
		Name: "claim-only", Version: "v1", Description: "test",
		Nodes: []nodepkg.TemplateNodeDef{{
			Type:     "t",
			Executor: "",
			ClaimProducers: []nodepkg.NodeClaimProducerRef{
				{Name: "alpha", Selector: "x", Intent: "rw"},
				{Name: "beta", Selector: "y", Intent: "r"},
			},
		}},
	})
	inst := pcCreateInstance(ctx, t, f.persist, sum.ID, "ck-claim")
	claimNode := pcCreateNode(ctx, t, f, inst.ID, "")
	pcSeedFrame(ctx, t, f, inst.ID, claimNode.ID)

	args := PureCascadeArgs{
		Persist: f.persist, Queue: f.driver.Queue(),
		Clock: shared.SystemClock{}, Logger: shared.SilentLogger{},
	}

	firstCount, err := ProcessPureCascade(ctx, args)
	require.NoError(t, err)
	assert.Equal(t, 1, firstCount, "first tick prepares the existing stale run for claim routing")

	for i := 0; i < 4; i++ {
		c, err := ProcessPureCascade(ctx, args)
		require.NoError(t, err)
		assert.Equal(t, 0, c, "tick %d must not re-process an already-prepared claim run", i+2)
	}

	var inFlight int
	pgdbtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT COUNT(*) FROM rimsky_node_runs
		  WHERE node_id = $1
		    AND state IN ('pending','stale','running','held','parked')`,
		[]any{claimNode.ID}, &inFlight)
	assert.Equal(t, 1, inFlight, "five scheduler ticks must yield exactly one in-flight run, not one per tick")

	var required []string
	pgdbtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT required_claim_producers FROM rimsky_node_runs
		  WHERE node_id = $1 AND state = 'stale'`,
		[]any{claimNode.ID}, &required)
	assert.ElementsMatch(t, []string{"alpha", "beta"}, required,
		"the reused stale run carries the claim-producer routing")

	var latest *persistence.NodeRunLatest
	inTxTest(t, ctx, f.persist, func(tx persistence.Tx) error {
		r, err := f.persist.Nodes().GetLatestRunForNode(ctx, claimNode.ID, tx)
		latest = r
		return err
	})
	require.NotNil(t, latest)
	assert.Equal(t, cascade.NodeStateStale, latest.State)

	var evs persistence.EventListResult
	inTxTest(t, ctx, f.persist, func(tx persistence.Tx) error {
		r, err := f.persist.Events().List(ctx, persistence.EventListFilter{
			NodeID: &claimNode.ID, KindIn: []string{"terminal/success"},
		}, persistence.ListPagination{Limit: 100}, tx)
		evs = r
		return err
	})
	assert.Empty(t, evs.Events, "a claim-only node must not settle before its claims are acquired")
}

func TestProcessPureCascade_CascadesToDependents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newPureCascadeFixture(t)

	tpl := insertDeployedTemplate(ctx, t, f.persist, nodepkg.TemplateSpec{
		Name: "alpha-cascade", Version: "v1",
		Nodes: []nodepkg.TemplateNodeDef{
			{Type: "pure-a"},
			{Type: "worker-b", Executor: "worker",
				Subscribes: []nodepkg.SubscriptionEntry{{Node: "pure-a", Type: "terminal/*", ForceUpstreamRefresh: nodepkg.BoolPtr(false)}}},
		},
	})
	inst := pcCreateInstance(ctx, t, f.persist, tpl.ID, "ck-1")

	pureA := pcCreateNodeWithType(ctx, t, f, inst.ID, "pure-a", "")
	execB := pcCreateNodeWithType(ctx, t, f, inst.ID, "worker-b", "worker")
	pcSeedFrame(ctx, t, f, inst.ID, execB.ID)
	_ = execB

	q := &fakeQueue{}
	count, err := ProcessPureCascade(ctx, pcArgs(f.persist, q))
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	var latestA *persistence.NodeRunLatest
	inTxTest(t, ctx, f.persist, func(tx persistence.Tx) error {
		r, err := f.persist.Nodes().GetLatestRunForNode(ctx, pureA.ID, tx)
		latestA = r
		return err
	})
	require.NotNil(t, latestA)
	assert.Equal(t, cascade.NodeStateFresh, latestA.State)

	var evs persistence.EventListResult
	inTxTest(t, ctx, f.persist, func(tx persistence.Tx) error {
		r, err := f.persist.Events().List(ctx, persistence.EventListFilter{
			KindIn: []string{"terminal/success"},
		}, persistence.ListPagination{Limit: 100}, tx)
		evs = r
		return err
	})
	require.Len(t, evs.Events, 1)
	require.NotNil(t, evs.Events[0].NodeID)
	assert.Equal(t, pureA.ID, *evs.Events[0].NodeID)
}

type nthTransactionFailsTables struct {
	persistence.Tables
	callCount  int
	failOnCall int
	failErr    error
}

func (n *nthTransactionFailsTables) Transaction(ctx context.Context, fn func(ctx context.Context, tx persistence.Tx) error) error {
	n.callCount++
	if n.callCount == n.failOnCall {
		return n.failErr
	}
	return n.Tables.Transaction(ctx, fn)
}

func TestProcessPureCascade_TemplateLookupTransactionErrorDoesNotSettleClaimNode(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newPureCascadeFixture(t)

	sum := insertDeployedTemplate(ctx, t, f.persist, nodepkg.TemplateSpec{
		Name: "claim-only-lookup-error", Version: "v1", Description: "test",
		Nodes: []nodepkg.TemplateNodeDef{{
			Type:     "t",
			Executor: "",
			ClaimProducers: []nodepkg.NodeClaimProducerRef{
				{Name: "alpha", Selector: "x", Intent: "rw"},
			},
		}},
	})
	inst := pcCreateInstance(ctx, t, f.persist, sum.ID, "ck-claim-lookup-error")
	claimNode := pcCreateNode(ctx, t, f, inst.ID, "")
	pcSeedFrame(ctx, t, f, inst.ID, claimNode.ID)

	failingErr := fmt.Errorf("simulated transient DB error during template lookup")
	flaky := &nthTransactionFailsTables{
		Tables:     f.persist,
		failOnCall: 2,
		failErr:    failingErr,
	}

	q := &fakeQueue{}
	count, err := ProcessPureCascade(ctx, PureCascadeArgs{
		Persist: flaky, Queue: q, Clock: shared.SystemClock{}, Logger: shared.SilentLogger{},
	})
	require.NoError(t, err, "a per-row lookup failure must not abort the whole scheduler tick")
	assert.Equal(t, 0, count, "a lookup failure must not count as a prepared/transitioned dispatch")

	var required []string
	pgdbtest.QueryRowForTest(ctx, t, f.driver,
		`SELECT required_claim_producers FROM rimsky_node_runs WHERE node_id = $1`,
		[]any{claimNode.ID}, &required)
	assert.Empty(t, required,
		"a claim-bearing node must not have its claim routing prepared when the template lookup failed")

	var latest *persistence.NodeRunLatest
	inTxTest(t, ctx, f.persist, func(tx persistence.Tx) error {
		r, err := f.persist.Nodes().GetLatestRunForNode(ctx, claimNode.ID, tx)
		latest = r
		return err
	})
	require.NotNil(t, latest)
	assert.Equal(t, cascade.NodeStateStale, latest.State,
		"a claim-bearing node must not transition to fresh/settled when its claims could not be routed")

	var evs persistence.EventListResult
	inTxTest(t, ctx, f.persist, func(tx persistence.Tx) error {
		r, err := f.persist.Events().List(ctx, persistence.EventListFilter{
			NodeID: &claimNode.ID, KindIn: []string{"terminal/success"},
		}, persistence.ListPagination{Limit: 100}, tx)
		evs = r
		return err
	})
	assert.Empty(t, evs.Events,
		"a transient template-lookup DB error must never settle a claim-bearing node terminal/success without acquiring its claims")
}
