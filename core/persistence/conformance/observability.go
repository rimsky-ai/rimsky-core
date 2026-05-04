package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	nodepkg "github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/persistence"
	"github.com/fallguy/rimsky/core/shared"
)

// testInstancesFindAnyByInstanceKey verifies the cross-driver behavior
// of InstanceStore.FindAnyByInstanceKey: returns (nil, nil) when no
// matching row exists; resolves a row inserted via Create.
func testInstancesFindAnyByInstanceKey(t *testing.T, d persistence.Driver) {
	t.Helper()
	defer d.Close()
	ctx := context.Background()
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := d.Store()

	row, err := store.Instances().FindAnyByInstanceKey(ctx, "no-such-key", nil)
	if err != nil {
		t.Fatalf("FindAnyByInstanceKey unknown: %v", err)
	}
	if row != nil {
		t.Fatalf("FindAnyByInstanceKey unknown returned %+v, want nil", row)
	}

	tmpl := "sha256-" + uuid.NewString()
	if err := store.Templates().Insert(ctx, persistence.TemplateInsertInput{
		ID: tmpl,
		Spec: nodepkg.TemplateSpec{
			Name: "find-key", Version: "1",
			FrameResolution: nodepkg.FrameResolutionSerialQueue,
			FrameTimeoutMs:  600000,
			Nodes:           []nodepkg.TemplateNodeDef{{Type: "n", Executor: "e"}},
		},
		State:  persistence.TemplateStateRegistered,
		Source: "direct",
	}, nil); err != nil {
		t.Fatalf("template insert: %v", err)
	}
	key := "instance-key-1"
	id := uuid.New()
	_, err = store.Instances().Create(ctx, persistence.InstanceCreateInput{
		ID:           id,
		TemplateHash: tmpl,
		InstanceKey:  &key,
		Params:       map[string]any{},
	}, nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.Instances().FindAnyByInstanceKey(ctx, key, nil)
	if err != nil {
		t.Fatalf("FindAnyByInstanceKey: %v", err)
	}
	if got == nil || got.ID != id {
		t.Fatalf("FindAnyByInstanceKey = %+v, want id=%s", got, id)
	}
}

// testStoreLifecycleListByStore verifies StoreLifecycleStore.ListByStore
// returns rows for any scope when filtered by store registration name.
func testStoreLifecycleListByStore(t *testing.T, d persistence.Driver) {
	t.Helper()
	defer d.Close()
	ctx := context.Background()
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := d.Store()

	rows, err := store.StoreLifecycle().ListByStore(ctx, "no-store", nil)
	if err != nil {
		t.Fatalf("ListByStore empty: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("ListByStore empty returned %d rows", len(rows))
	}

	if err := store.StoreLifecycle().Upsert(ctx, persistence.StoreLifecycleRow{
		StoreRegistrationName: "store-a",
		ScopeKind:             persistence.StoreLifecycleScopeTemplate,
		ScopeID:               "tpl-1",
		State:                 persistence.StoreLifecycleStateRegistered,
	}, nil); err != nil {
		t.Fatalf("upsert tpl: %v", err)
	}
	if err := store.StoreLifecycle().Upsert(ctx, persistence.StoreLifecycleRow{
		StoreRegistrationName: "store-a",
		ScopeKind:             persistence.StoreLifecycleScopeInstance,
		ScopeID:               uuid.New().String(),
		State:                 persistence.StoreLifecycleStateCreated,
	}, nil); err != nil {
		t.Fatalf("upsert inst: %v", err)
	}
	rows, err = store.StoreLifecycle().ListByStore(ctx, "store-a", nil)
	if err != nil {
		t.Fatalf("ListByStore: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("ListByStore = %d rows, want 2", len(rows))
	}
}

// testEventsListDescending checks that events are returned newest-first
// per spec §1.2.5.
func testEventsListDescending(t *testing.T, d persistence.Driver) {
	t.Helper()
	defer d.Close()
	ctx := context.Background()
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := d.Store()

	tmpl := "sha256-" + uuid.NewString()
	if err := store.Templates().Insert(ctx, persistence.TemplateInsertInput{
		ID: tmpl,
		Spec: nodepkg.TemplateSpec{
			Name: "events-desc", Version: "1",
			FrameResolution: nodepkg.FrameResolutionSerialQueue,
			FrameTimeoutMs:  600000,
			Nodes:           []nodepkg.TemplateNodeDef{{Type: "n", Executor: "e"}},
		},
		State:  persistence.TemplateStateRegistered,
		Source: "direct",
	}, nil); err != nil {
		t.Fatalf("template insert: %v", err)
	}
	id := uuid.New()
	if _, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{
		ID:           id,
		TemplateHash: tmpl,
		Params:       map[string]any{},
	}, nil); err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, k := range []string{"a", "b", "c"} {
		if err := store.Events().Append(ctx, persistence.EventAppendInput{
			InstanceID: &id,
			Kind:       k,
			Payload:    map[string]any{},
		}, nil); err != nil {
			t.Fatalf("append %s: %v", k, err)
		}
	}
	res, err := store.Events().List(ctx, persistence.EventListFilter{InstanceID: &id}, persistence.ListPagination{Limit: 10}, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res.Events) != 3 {
		t.Fatalf("events = %d, want 3", len(res.Events))
	}
	if res.Events[0].Kind != "c" || res.Events[2].Kind != "a" {
		t.Fatalf("events kinds = %v %v %v, want [c, b, a] (DESC)",
			res.Events[0].Kind, res.Events[1].Kind, res.Events[2].Kind)
	}
}

// testSchedulesDenseSameTimestampPagination guards the round-2
// scheduleCursor fix. The cursor pairs (next_fire_at, node_id) and
// the predicate is `(next_fire_at, node_id) > ($t, $id)` — without
// the secondary node_id key, paginating across rows that share a
// next_fire_at can either drop or duplicate entries depending on the
// driver's tie-breaking. We register multiple schedules with the same
// next_fire_at, page through with Limit=2, and assert all rows surface
// across pages with no drops, no duplicates.
func testSchedulesDenseSameTimestampPagination(t *testing.T, d persistence.Driver) {
	t.Helper()
	defer d.Close()
	ctx := context.Background()
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := d.Store()

	// Seed a template + instance so the FK on rimsky_schedules.node_id
	// is satisfied. The frame seeded by seedFixtureSet is unused here —
	// schedules attach to nodes regardless of frame state.
	tmpl := "sha256-" + uuid.NewString()
	if err := store.Templates().Insert(ctx, persistence.TemplateInsertInput{
		ID: tmpl,
		Spec: nodepkg.TemplateSpec{
			Name: "schedules-dense", Version: "1",
			FrameResolution: nodepkg.FrameResolutionSerialQueue,
			FrameTimeoutMs:  600000,
			Nodes:           []nodepkg.TemplateNodeDef{{Type: "n", Executor: "e"}},
		},
		State:  persistence.TemplateStateRegistered,
		Source: "direct",
	}, nil); err != nil {
		t.Fatalf("template insert: %v", err)
	}
	instID := uuid.New()
	if _, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{
		ID:           instID,
		TemplateHash: tmpl,
		Params:       map[string]any{},
	}, nil); err != nil {
		t.Fatalf("instance create: %v", err)
	}

	// Register 5 schedules sharing the same next_fire_at so dense
	// pagination has to break ties via node_id. 5 with Limit=2 forces
	// 3 page boundaries, exercising both first-page and mid-traverse.
	const numSchedules = 5
	nextFire := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	expected := make(map[string]struct{}, numSchedules)
	for i := 0; i < numSchedules; i++ {
		nodeID := uuid.New()
		if _, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID:           nodeID,
			InstanceID:   instID,
			NodeType:     "n",
			Executor:     "e",
			Dependencies: []shared.UUID{},
		}, nil); err != nil {
			t.Fatalf("node create: %v", err)
		}
		if err := store.Schedules().Register(ctx, persistence.ScheduleRegisterInput{
			NodeID:     nodeID,
			CronExpr:   "* * * * *",
			NextFireAt: nextFire,
		}, nil); err != nil {
			t.Fatalf("schedule register: %v", err)
		}
		expected[nodeID.String()] = struct{}{}
	}

	seen := map[string]int{}
	cursor := ""
	for page := 0; page < numSchedules+2; page++ {
		res, err := store.Schedules().ListForObservability(ctx,
			persistence.ScheduleListFilter{},
			persistence.ListPagination{Limit: 2, Cursor: cursor},
			nil,
		)
		if err != nil {
			t.Fatalf("ListForObservability page=%d: %v", page, err)
		}
		for _, row := range res.Rows {
			seen[row.NodeID.String()]++
		}
		cursor = res.NextCursor
		if cursor == "" {
			break
		}
	}
	if cursor != "" {
		t.Fatalf("pagination did not terminate after numSchedules+2 pages; cursor=%q", cursor)
	}

	if len(seen) != numSchedules {
		t.Fatalf("dense-pagination saw %d distinct rows; want %d. seen=%v", len(seen), numSchedules, seen)
	}
	for id := range expected {
		if seen[id] != 1 {
			t.Fatalf("dense-pagination: row %s seen %d times (want 1)", id, seen[id])
		}
	}
}
