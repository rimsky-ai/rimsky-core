// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package conformance

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/foundation/spec"
)

// testInstancesFindAnyByInstanceKey verifies the cross-driver behavior
// of InstanceTable.FindAnyByInstanceKey: returns (nil, nil) when no
// matching row exists; resolves a row inserted via Create.
func testInstancesFindAnyByInstanceKey(t *testing.T, d persistence.Database) {
	t.Helper()
	defer d.Close()
	ctx := context.Background()
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := d.Tables()

	var row *persistence.InstanceRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.Instances().FindAnyByInstanceKey(ctx, "no-such-key", tx)
		row = r
		return err
	}); err != nil {
		t.Fatalf("FindAnyByInstanceKey unknown: %v", err)
	}
	if row != nil {
		t.Fatalf("FindAnyByInstanceKey unknown returned %+v, want nil", row)
	}

	tmpl := "sha256-" + uuid.NewString()
	key := "instance-key-1"
	id := uuid.New()
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if err := store.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: tmpl,
			Spec: spec.TemplateSpec{
				Name: "find-key", Version: "1",
				FrameResolutionMode: spec.FrameResolutionSerialQueue,
				FrameTimeoutMs:      600000,
				Nodes:               []spec.TemplateNodeDef{{Type: "n", Executor: "e"}},
			},
			State:  persistence.TemplateStateRegistered,
			Source: "direct",
		}, tx); err != nil {
			return err
		}
		mainScopeID := seedMainRunScopeForInstance(ctx, t, tx, store, id)
		_, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID:             id,
			TemplateHash:   tmpl,
			InstanceKey:    &key,
			Params:         map[string]any{},
			MainRunScopeID: mainScopeID,
		}, tx)
		return err
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	var got *persistence.InstanceRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.Instances().FindAnyByInstanceKey(ctx, key, tx)
		got = r
		return err
	}); err != nil {
		t.Fatalf("FindAnyByInstanceKey: %v", err)
	}
	if got == nil || got.ID != id {
		t.Fatalf("FindAnyByInstanceKey = %+v, want id=%s", got, id)
	}
}

// testStoreLifecycleListByStore verifies LifecycleIdempotencyTable.ListByStore
// returns rows for any scope when filtered by store registration name.
func testStoreLifecycleListByStore(t *testing.T, d persistence.Database) {
	t.Helper()
	defer d.Close()
	ctx := context.Background()
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := d.Tables()

	var rows []persistence.LifecycleIdempotencyRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.LifecycleIdempotency().ListByStore(ctx, "no-store", tx)
		rows = r
		return err
	}); err != nil {
		t.Fatalf("ListByStore empty: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("ListByStore empty returned %d rows", len(rows))
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.LifecycleIdempotency().Upsert(ctx, persistence.LifecycleIdempotencyRow{
			StoreRegistrationName: "store-a",
			ScopeKind:             persistence.LifecycleIdempotencyScopeTemplate,
			ScopeID:               "tpl-1",
			State:                 persistence.LifecycleIdempotencyStateRegistered,
		}, tx)
	}); err != nil {
		t.Fatalf("upsert tpl: %v", err)
	}
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.LifecycleIdempotency().Upsert(ctx, persistence.LifecycleIdempotencyRow{
			StoreRegistrationName: "store-a",
			ScopeKind:             persistence.LifecycleIdempotencyScopeInstance,
			ScopeID:               uuid.New().String(),
			State:                 persistence.LifecycleIdempotencyStateCreated,
		}, tx)
	}); err != nil {
		t.Fatalf("upsert inst: %v", err)
	}
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.LifecycleIdempotency().ListByStore(ctx, "store-a", tx)
		rows = r
		return err
	}); err != nil {
		t.Fatalf("ListByStore: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("ListByStore = %d rows, want 2", len(rows))
	}
}

// testEventsListDescending checks that events are returned newest-first
// per spec §1.2.5.
func testEventsListDescending(t *testing.T, d persistence.Database) {
	t.Helper()
	defer d.Close()
	ctx := context.Background()
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := d.Tables()

	tmpl := "sha256-" + uuid.NewString()
	id := uuid.New()
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if err := store.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: tmpl,
			Spec: spec.TemplateSpec{
				Name: "events-desc", Version: "1",
				FrameResolutionMode: spec.FrameResolutionSerialQueue,
				FrameTimeoutMs:      600000,
				Nodes:               []spec.TemplateNodeDef{{Type: "n", Executor: "e"}},
			},
			State:  persistence.TemplateStateRegistered,
			Source: "direct",
		}, tx); err != nil {
			return err
		}
		mainScopeID := seedMainRunScopeForInstance(ctx, t, tx, store, id)
		_, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID:             id,
			TemplateHash:   tmpl,
			Params:         map[string]any{},
			MainRunScopeID: mainScopeID,
		}, tx)
		return err
	}); err != nil {
		t.Fatalf("template/instance insert: %v", err)
	}
	for _, k := range []string{"a", "b", "c"} {
		k := k
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			return store.Events().Append(ctx, persistence.EventAppendInput{
				InstanceID: &id,
				Kind:       k,
				Payload:    map[string]any{},
			}, tx)
		}); err != nil {
			t.Fatalf("append %s: %v", k, err)
		}
	}
	var res persistence.EventListResult
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.Events().List(ctx, persistence.EventListFilter{InstanceID: &id}, persistence.ListPagination{Limit: 10}, tx)
		res = r
		return err
	}); err != nil {
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

// (testSchedulesDenseSameTimestampPagination retired by the 2026-05-15
// data-platform-extensions plan B10 / D7 / E16 schedule-retirement
// cascade. The rimsky_schedules table and the
// `ScheduleTable.ListForObservability` helper it exercised are gone;
// cron firing is owned by `sensors/sensor-cron/`.)
