package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/internal/pgtest"
	nodepkg "github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/shared"
	"github.com/fallguy/rimsky/core/storage"
	pgstorage "github.com/fallguy/rimsky/core/storage/postgres"
)

// --- Test helpers -----------------------------------------------------

// tickerFixture bundles a real Postgres-backed storage plus a seeded
// template + instance so each test can insert nodes and schedule rows
// directly through the concrete storage substores.
type tickerFixture struct {
	sb       storage.StorageBackend
	instance storage.InstanceRow
}

func newTickerFixture(t *testing.T) (*tickerFixture, func()) {
	t.Helper()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	sb := pgstorage.New(pool)

	tpl := insertDeployedTemplate(ctx, t, sb, nodepkg.TemplateSpec{
		Name: "sched-ticker-" + uuid.NewString(), Version: "v1",
		Nodes: []nodepkg.TemplateNodeDef{},
	})
	ck := "ck-" + uuid.NewString()
	inst, err := sb.Instances().Create(ctx, storage.InstanceCreateInput{
		ID: uuid.New(), TemplateHash: tpl.ID, InstanceKey: &ck,
		Params: map[string]any{},
	}, nil)
	require.NoError(t, err)

	return &tickerFixture{sb: sb, instance: inst}, teardown
}

// addNode inserts a node with a random ID and returns its ID.
func (f *tickerFixture) addNode(t *testing.T) shared.UUID {
	t.Helper()
	ctx := context.Background()
	n, err := f.sb.Nodes().Create(ctx, storage.NodeCreateInput{
		ID: uuid.New(), InstanceID: f.instance.ID, NodeType: "t",
		Executor: "worker",
	}, nil)
	require.NoError(t, err)
	return n.ID
}

// addSchedule registers a schedule row for nodeID with the given cron and
// next_fire_at. Register acts as an upsert.
func (f *tickerFixture) addSchedule(t *testing.T, nodeID shared.UUID, cronExpr string, nextFireAt time.Time) {
	t.Helper()
	require.NoError(t, f.sb.Schedules().Register(context.Background(), storage.ScheduleRegisterInput{
		NodeID: nodeID, CronExpr: cronExpr, NextFireAt: nextFireAt,
	}, nil))
}

// eventsFor returns all events for nodeID (most recent first).
func (f *tickerFixture) eventsFor(t *testing.T, nodeID shared.UUID) []storage.EventRow {
	t.Helper()
	res, err := f.sb.Events().List(context.Background(),
		storage.EventListFilter{NodeID: &nodeID},
		storage.ListPagination{Limit: 100}, nil)
	require.NoError(t, err)
	return res.Events
}

// scheduleFor returns the schedule row for nodeID, or nil if not present.
func (f *tickerFixture) scheduleFor(t *testing.T, nodeID shared.UUID) *storage.ScheduleRow {
	t.Helper()
	all, err := f.sb.Schedules().ListAll(context.Background(), nil)
	require.NoError(t, err)
	for i := range all {
		if all[i].NodeID == nodeID {
			return &all[i]
		}
	}
	return nil
}

// --- Dispatcher fake (scheduler-level; storage stays real) ----------

type fakeDispatcher struct {
	mu    sync.Mutex
	calls []InvalidateRequest
	err   error
}

func (f *fakeDispatcher) EmitInvalidate(ctx context.Context, req InvalidateRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req)
	return f.err
}

func (f *fakeDispatcher) snapshot() []InvalidateRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]InvalidateRequest, len(f.calls))
	copy(out, f.calls)
	return out
}

// --- NextFireAt tests --------------------------------------------------

func TestNextFireAt_ValidCron(t *testing.T) {
	// */5 * * * * from 10:02 UTC → next fire 10:05 UTC.
	from := time.Date(2026, 4, 22, 10, 2, 0, 0, time.UTC)
	got, err := NextFireAt("*/5 * * * *", from)
	require.NoError(t, err)
	want := time.Date(2026, 4, 22, 10, 5, 0, 0, time.UTC)
	assert.True(t, got.Equal(want), "expected %v, got %v", want, got)
}

func TestNextFireAt_InvalidCron(t *testing.T) {
	_, err := NextFireAt("garbage", time.Now())
	require.Error(t, err)
}

// --- ProcessSchedules tests -------------------------------------------

func TestProcessSchedules_NothingDue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, teardown := newTickerFixture(t)
	t.Cleanup(teardown)

	// A schedule that is far in the future.
	now := time.Date(2026, 4, 22, 10, 0, 0, 0, time.UTC)
	nodeID := f.addNode(t)
	f.addSchedule(t, nodeID, "*/5 * * * *", now.Add(1*time.Hour))

	disp := &fakeDispatcher{}
	clock := shared.NewControllableClock(now)

	fired, err := ProcessSchedules(ctx, f.sb, disp, clock, shared.SilentLogger{})
	require.NoError(t, err)
	assert.Equal(t, 0, fired)
	assert.Empty(t, disp.snapshot())
	assert.Empty(t, f.eventsFor(t, nodeID))

	// Schedule row unchanged (last_fired_at still nil).
	row := f.scheduleFor(t, nodeID)
	require.NotNil(t, row)
	assert.Nil(t, row.LastFiredAt)
}

func TestProcessSchedules_FiresOneDue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, teardown := newTickerFixture(t)
	t.Cleanup(teardown)

	now := time.Date(2026, 4, 22, 10, 7, 0, 0, time.UTC)
	nodeID := f.addNode(t)
	f.addSchedule(t, nodeID, "*/5 * * * *", now.Add(-1*time.Minute)) // due

	disp := &fakeDispatcher{}
	clock := shared.NewControllableClock(now)

	fired, err := ProcessSchedules(ctx, f.sb, disp, clock, shared.SilentLogger{})
	require.NoError(t, err)
	assert.Equal(t, 1, fired)

	// Dispatcher saw exactly one invalidate to the node itself.
	calls := disp.snapshot()
	require.Len(t, calls, 1)
	assert.Equal(t, nodeID, calls[0].TargetNodeID)
	assert.Nil(t, calls[0].SourceNodeID)
	assert.Equal(t, "schedule_fired", calls[0].Reason)

	// next_fire_at advanced via RecordFired; last_fired_at populated.
	row := f.scheduleFor(t, nodeID)
	require.NotNil(t, row)
	expectedNext := time.Date(2026, 4, 22, 10, 10, 0, 0, time.UTC)
	assert.True(t, row.NextFireAt.Equal(expectedNext),
		"expected next=%v, got %v", expectedNext, row.NextFireAt)
	require.NotNil(t, row.LastFiredAt)

	// schedule_fired event logged.
	evs := f.eventsFor(t, nodeID)
	require.Len(t, evs, 1)
	assert.Equal(t, "schedule_fired", evs[0].Kind)
	require.NotNil(t, evs[0].NodeID)
	assert.Equal(t, nodeID, *evs[0].NodeID)
}

func TestProcessSchedules_DispatcherErrorLogsScheduleDispatchFailed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, teardown := newTickerFixture(t)
	t.Cleanup(teardown)

	now := time.Date(2026, 4, 22, 10, 7, 0, 0, time.UTC)
	nodeA := f.addNode(t)
	nodeB := f.addNode(t)
	f.addSchedule(t, nodeA, "*/5 * * * *", now.Add(-2*time.Minute))
	f.addSchedule(t, nodeB, "*/5 * * * *", now.Add(-1*time.Minute))

	disp := &fakeDispatcher{err: errors.New("downstream unreachable")}
	clock := shared.NewControllableClock(now)

	fired, err := ProcessSchedules(ctx, f.sb, disp, clock, shared.SilentLogger{})
	require.NoError(t, err)
	// Both rows errored → fired==0, but processing continued (both were attempted).
	assert.Equal(t, 0, fired)

	// Dispatcher was called for both rows.
	assert.Len(t, disp.snapshot(), 2)

	// Both rows had RecordFired called before dispatch (last_fired_at set).
	rowA := f.scheduleFor(t, nodeA)
	require.NotNil(t, rowA)
	require.NotNil(t, rowA.LastFiredAt, "RecordFired should have advanced row A")
	rowB := f.scheduleFor(t, nodeB)
	require.NotNil(t, rowB)
	require.NotNil(t, rowB.LastFiredAt, "RecordFired should have advanced row B")

	// Two schedule_dispatch_failed events (one per node).
	evsA := f.eventsFor(t, nodeA)
	require.Len(t, evsA, 1)
	assert.Equal(t, "schedule_dispatch_failed", evsA[0].Kind)
	assert.Contains(t, evsA[0].Payload, "error")
	evsB := f.eventsFor(t, nodeB)
	require.Len(t, evsB, 1)
	assert.Equal(t, "schedule_dispatch_failed", evsB[0].Kind)
	assert.Contains(t, evsB[0].Payload, "error")
}

func TestProcessSchedules_InvalidCronSkipsWithEvent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, teardown := newTickerFixture(t)
	t.Cleanup(teardown)

	now := time.Date(2026, 4, 22, 10, 7, 0, 0, time.UTC)
	nodeID := f.addNode(t)
	f.addSchedule(t, nodeID, "garbage", now.Add(-1*time.Minute))

	disp := &fakeDispatcher{}
	clock := shared.NewControllableClock(now)

	fired, err := ProcessSchedules(ctx, f.sb, disp, clock, shared.SilentLogger{})
	require.NoError(t, err)
	assert.Equal(t, 0, fired)

	// No dispatch attempt.
	assert.Empty(t, disp.snapshot())

	// No RecordFired call (we bail before advancing) → last_fired_at still nil.
	row := f.scheduleFor(t, nodeID)
	require.NotNil(t, row)
	assert.Nil(t, row.LastFiredAt)

	// One schedule_dispatch_failed event logged.
	evs := f.eventsFor(t, nodeID)
	require.Len(t, evs, 1)
	assert.Equal(t, "schedule_dispatch_failed", evs[0].Kind)
	require.NotNil(t, evs[0].NodeID)
	assert.Equal(t, nodeID, *evs[0].NodeID)
}
