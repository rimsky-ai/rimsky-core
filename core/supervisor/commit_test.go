// Tests for Commit. Each test spins up a fresh Postgres container via pgtest,
// stands up a minimal template → instance → node graph, and exercises Commit
// against a real postgres.ResourceRegistry-backed inline-jsonb Resource.
package supervisor_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/internal/pgtest"
	nodepkg "github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/qualityrule"
	queuepg "github.com/fallguy/rimsky/core/queue/postgres"
	"github.com/fallguy/rimsky/core/resource"
	"github.com/fallguy/rimsky/core/resource/inlinejsonb"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	storagepg "github.com/fallguy/rimsky/core/storage/postgres"
	"github.com/fallguy/rimsky/core/supervisor"
)

// Always-fail quality rule used by the quality-rejection test.
type alwaysFailRule struct{}

func (alwaysFailRule) Evaluate(_ context.Context, in qualityrule.EvalInput) (bool, string, error) {
	d, _ := in.Cfg["details"].(string)
	if d == "" {
		d = "commit-test-failed"
	}
	return false, d, nil
}

func init() {
	qualityrule.Register("supervisor_commit_test_always_fail", alwaysFailRule{})
}

// fixture is the common scaffolding for supervisor tests.
type fixture struct {
	t        *testing.T
	pool     *pgxpool.Pool
	sb       *storagepg.PostgresStorageBackend
	q        *queuepg.Queue
	clock    *shared.ControllableClock
	log      *shared.CapturingLogger
	instance shared.UUID
	template shared.UUID
}

func newFixture(t *testing.T, nodeTypes []nodepkg.TemplateNodeDef) *fixture {
	t.Helper()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	sb := storagepg.New(pool)
	qq := queuepg.New(pool)

	tplSum, err := sb.Templates().Deploy(ctx, nodepkg.TemplateSpec{
		Name: "sup-tpl-" + uuid.NewString()[:8], Version: "v1",
		Nodes: nodeTypes,
	}, nil)
	require.NoError(t, err)

	inst, err := sb.Instances().Create(ctx, storage.InstanceCreateInput{
		TemplateID: tplSum.ID, ConsumerKey: "ck-" + uuid.NewString()[:8],
		Params: map[string]any{},
	}, nil)
	require.NoError(t, err)

	return &fixture{
		t:        t,
		pool:     pool,
		sb:       sb,
		q:        qq,
		clock:    shared.NewControllableClock(time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)),
		log:      shared.NewCapturingLogger(),
		instance: inst.ID,
		template: tplSum.ID,
	}
}

// addNode creates a node with the given type/executor/dependencies,
// transitions it to running, and returns the row.
func (f *fixture) addRunningNode(nodeType, executor string, deps ...shared.UUID) storage.NodeRow {
	f.t.Helper()
	ctx := context.Background()
	n, err := f.sb.Nodes().Create(ctx, storage.NodeCreateInput{
		ID: uuid.New(), InstanceID: f.instance, NodeType: nodeType,
		Executor: executor, Dependencies: deps,
	}, nil)
	require.NoError(f.t, err)
	// stale → running via dispatch_claimed.
	err = f.sb.Nodes().UpdateState(ctx, n.ID, shared.NodeStateRunning, nodepkg.ReasonDispatchClaimed, nil)
	require.NoError(f.t, err)
	out, err := f.sb.Nodes().Get(ctx, n.ID, nil)
	require.NoError(f.t, err)
	return *out
}

// addStaleNode creates a node and leaves it in the default (stale) state.
func (f *fixture) addStaleNode(nodeType, executor string, deps ...shared.UUID) storage.NodeRow {
	f.t.Helper()
	ctx := context.Background()
	n, err := f.sb.Nodes().Create(ctx, storage.NodeCreateInput{
		ID: uuid.New(), InstanceID: f.instance, NodeType: nodeType,
		Executor: executor, Dependencies: deps,
	}, nil)
	require.NoError(f.t, err)
	return n
}

// buildInlineResource wires an inline-jsonb Resource for the given node.
func (f *fixture) buildInlineResource(nodeID shared.UUID, rules []qualityrule.Spec) (shared.UUID, resource.Resource) {
	f.t.Helper()
	ctx := context.Background()
	row, err := f.sb.Resources().Create(ctx, storage.ResourceCreateInput{
		ResourcePath: []string{"t", nodeID.String()[:8]},
		OwnerNodeID:  nodeID,
		KeepVersions: 2,
	}, nil)
	require.NoError(f.t, err)

	factory := inlinejsonb.Factory{StorageRegistry: f.sb.Resources()}
	res, err := factory.Create(resource.Config{
		"keep_versions":   2,
		"_resource_id":    row.ID.String(),
		"_path":           []string{"t", nodeID.String()[:8]},
		"_owner_node_id":  nodeID.String(),
	}, rules, nil)
	require.NoError(f.t, err)
	return row.ID, res
}

// resolver returns a GetResource callback for the supervisor: maps row id
// back to a pre-built Resource.
func resolver(m map[shared.UUID]resource.Resource) func(ctx context.Context, rid shared.UUID) (resource.Resource, error) {
	return func(_ context.Context, rid shared.UUID) (resource.Resource, error) {
		r, ok := m[rid]
		if !ok {
			return nil, fmt.Errorf("resolver: no resource for %s", rid)
		}
		return r, nil
	}
}

// eventKinds returns the Kind values for every event row on the node, oldest first.
func (f *fixture) eventKinds(nodeID shared.UUID) []string {
	f.t.Helper()
	ctx := context.Background()
	nid := nodeID
	res, err := f.sb.Events().List(ctx, storage.EventListFilter{NodeID: &nid},
		storage.ListPagination{Limit: 1000}, nil)
	require.NoError(f.t, err)
	// Events().List orders newest-first per the usual pattern; reverse to oldest-first.
	kinds := make([]string, 0, len(res.Events))
	for i := len(res.Events) - 1; i >= 0; i-- {
		kinds = append(kinds, res.Events[i].Kind)
	}
	return kinds
}

func containsString(xs []string, target string) bool {
	for _, x := range xs {
		if x == target {
			return true
		}
	}
	return false
}

// pendingDispatchForNode returns the pending dispatch row for the node, if any.
func (f *fixture) pendingDispatchForNode(nodeID shared.UUID) *shared.DispatchRow {
	f.t.Helper()
	ctx := context.Background()
	var (
		id              shared.UUID
		executor        string
		tags            []string
		enqueuedAt      time.Time
		claimedBy       *string
		claimedAt       *time.Time
	)
	err := f.pool.QueryRow(ctx,
		`SELECT id, executor_name, concurrency_tags, enqueued_at, claimed_by, claimed_at
		   FROM rimsky_dispatch WHERE node_id = $1`, nodeID,
	).Scan(&id, &executor, &tags, &enqueuedAt, &claimedBy, &claimedAt)
	if err != nil {
		return nil
	}
	return &shared.DispatchRow{
		ID: id, NodeID: nodeID, ExecutorName: executor,
		ConcurrencyTags: tags, EnqueuedAt: enqueuedAt,
		ClaimedBy: claimedBy, ClaimedAt: claimedAt,
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestCommit_HappyPath_EmitsCommitEvent_TransitionsFresh_Cascades(t *testing.T) {
	t.Parallel()
	f := newFixture(t, []nodepkg.TemplateNodeDef{
		{Type: "producer", Executor: "worker"},
		{Type: "dependent", Executor: "worker"},
	})
	ctx := context.Background()

	producer := f.addRunningNode("producer", "worker")
	dep := f.addStaleNode("dependent", "worker", producer.ID)

	rid, res := f.buildInlineResource(producer.ID, nil)

	err := supervisor.Commit(ctx, supervisor.CommitArgs{
		Storage: f.sb, Queue: f.q, Clock: f.clock, Logger: f.log,
		NodeID: producer.ID, InstanceID: f.instance,
		Result:        map[string]any{"rows": []any{"a", "b"}},
		Changed:       true,
		ChangeSummary: "initial",
		GetResource:   resolver(map[shared.UUID]resource.Resource{rid: res}),
	})
	require.NoError(t, err)

	// Node transitioned to fresh, error state cleared.
	got, err := f.sb.Nodes().Get(ctx, producer.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateFresh, got.State)
	require.Equal(t, 0, got.RetryCounter)
	require.Equal(t, 0, got.ActionIndex)
	require.Equal(t, "", got.CurrentErrorClass)

	// Commit + work_completed event present.
	kinds := f.eventKinds(producer.ID)
	require.True(t, containsString(kinds, "commit"), "kinds=%v", kinds)
	require.True(t, containsString(kinds, "work_completed"), "kinds=%v", kinds)

	// Resource has a current version with our data.
	cur, err := res.CurrentVersion(ctx)
	require.NoError(t, err)
	require.NotNil(t, cur)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(cur.Data, &payload))
	require.Contains(t, payload, "rows")

	// Dependent recalculated: it's stale with all-fresh deps and an executor,
	// so RecalculateNode enqueues a dispatch row.
	dr := f.pendingDispatchForNode(dep.ID)
	require.NotNil(t, dr, "expected dependent to be enqueued")
	require.Equal(t, "worker", dr.ExecutorName)
}

func TestCommit_WithNoOpChangedFalse_LogsNoOpCommit_DoesNotCascade(t *testing.T) {
	t.Parallel()
	f := newFixture(t, []nodepkg.TemplateNodeDef{
		{Type: "producer", Executor: "worker"},
		{Type: "dependent", Executor: "worker"},
	})
	ctx := context.Background()

	producer := f.addRunningNode("producer", "worker")
	dep := f.addStaleNode("dependent", "worker", producer.ID)
	rid, res := f.buildInlineResource(producer.ID, nil)

	err := supervisor.Commit(ctx, supervisor.CommitArgs{
		Storage: f.sb, Queue: f.q, Clock: f.clock, Logger: f.log,
		NodeID: producer.ID, InstanceID: f.instance,
		Result:        map[string]any{"v": 1},
		Changed:       false,
		ChangeSummary: "unchanged",
		GetResource:   resolver(map[shared.UUID]resource.Resource{rid: res}),
	})
	require.NoError(t, err)

	// Node fresh; no_op_commit logged, commit NOT logged.
	got, err := f.sb.Nodes().Get(ctx, producer.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateFresh, got.State)

	kinds := f.eventKinds(producer.ID)
	require.True(t, containsString(kinds, "no_op_commit"), "kinds=%v", kinds)
	require.False(t, containsString(kinds, "commit"), "kinds=%v", kinds)
	require.True(t, containsString(kinds, "work_completed"), "kinds=%v", kinds)

	// Dependent NOT enqueued — no cascade on Changed=false.
	require.Nil(t, f.pendingDispatchForNode(dep.ID))
}

func TestCommit_NoOwnedResources_StillTransitionsFresh(t *testing.T) {
	t.Parallel()
	f := newFixture(t, []nodepkg.TemplateNodeDef{
		{Type: "probe", Executor: "worker"},
	})
	ctx := context.Background()
	probe := f.addRunningNode("probe", "worker")

	err := supervisor.Commit(ctx, supervisor.CommitArgs{
		Storage: f.sb, Queue: f.q, Clock: f.clock, Logger: f.log,
		NodeID: probe.ID, InstanceID: f.instance,
		Result:        nil,
		Changed:       true,
		ChangeSummary: "probe-ok",
		GetResource: func(_ context.Context, _ shared.UUID) (resource.Resource, error) {
			return nil, fmt.Errorf("no resources — should not be called")
		},
	})
	require.NoError(t, err)

	got, err := f.sb.Nodes().Get(ctx, probe.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateFresh, got.State)
	kinds := f.eventKinds(probe.ID)
	require.True(t, containsString(kinds, "work_completed"), "kinds=%v", kinds)
}

func TestCommit_QualityRejection_RoutesToOnError(t *testing.T) {
	t.Parallel()
	// Producer has a retry-then-give_up policy on quality_rule_failed — we
	// assert the commit flow lands us in retry + stale.
	producerDef := nodepkg.TemplateNodeDef{
		Type: "producer", Executor: "worker",
		ErrorTypes: map[string]nodepkg.ErrorTypePolicy{
			"quality_rule_failed": {
				Policy: []nodepkg.PolicyAction{
					{Action: "retry", Count: 1, Backoff: shared.BackoffLinear, BaseDelayMs: 50, MaxDelayMs: 50},
					{Action: "give_up"},
				},
			},
		},
	}
	f := newFixture(t, []nodepkg.TemplateNodeDef{producerDef})
	ctx := context.Background()

	producer := f.addRunningNode("producer", "worker")
	rules := []qualityrule.Spec{{
		Type: "supervisor_commit_test_always_fail",
		Config: map[string]any{"details": "nope"},
		Severity: shared.SeverityError,
	}}
	rid, res := f.buildInlineResource(producer.ID, rules)

	err := supervisor.Commit(ctx, supervisor.CommitArgs{
		Storage: f.sb, Queue: f.q, Clock: f.clock, Logger: f.log,
		NodeID: producer.ID, InstanceID: f.instance,
		Result: map[string]any{"x": 1}, Changed: true, ChangeSummary: "bad",
		GetResource: resolver(map[shared.UUID]resource.Resource{rid: res}),
	})
	require.NoError(t, err)

	// Node routed to OnError → retry → stale.
	got, err := f.sb.Nodes().Get(ctx, producer.ID, nil)
	require.NoError(t, err)
	require.Equal(t, shared.NodeStateStale, got.State)
	require.Equal(t, "quality_rule_failed", got.CurrentErrorClass)
	require.Equal(t, 1, got.RetryCounter)

	// quality_rule_failed event + error event both logged; work_completed NOT emitted.
	kinds := f.eventKinds(producer.ID)
	require.True(t, containsString(kinds, "quality_rule_failed"), "kinds=%v", kinds)
	require.True(t, containsString(kinds, "error"), "kinds=%v", kinds)
	require.False(t, containsString(kinds, "work_completed"), "kinds=%v", kinds)

	// Dispatch was re-enqueued with a future enqueued_at.
	dr := f.pendingDispatchForNode(producer.ID)
	require.NotNil(t, dr)
	require.True(t, dr.EnqueuedAt.After(f.clock.Now()) || dr.EnqueuedAt.Equal(f.clock.Now().Add(50*time.Millisecond)),
		"enqueued_at should be in the future; got %v (now=%v)", dr.EnqueuedAt, f.clock.Now())
}
