// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package conformance

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

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
				FrameTimeoutMs: 600000,
				Nodes:          []spec.TemplateNodeDef{{Type: "n", Executor: "e"}},
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
				FrameTimeoutMs: 600000,
				Nodes:          []spec.TemplateNodeDef{{Type: "n", Executor: "e"}},
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
	// @decision: event-log-kind-enum — real operational kinds (work_started,
	// state_transition, work_completed) exercise the read path through
	// ParseKindString at the unmarshal boundary; the three kinds are
	// stand-ins for "three rows" and ordering assertions rely on
	// insertion order.
	cases := []struct {
		kind events.Kind
		wire string
	}{
		{events.KindWorkStarted(), "work_started"},
		{events.KindStateTransition(), "state_transition"},
		{events.KindWorkCompleted(), "work_completed"},
	}
	for _, c := range cases {
		c := c
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			return store.Events().Append(ctx, persistence.EventAppendInput{
				InstanceID: &id,
				Kind:       c.kind,
				Payload:    map[string]any{},
			}, tx)
		}); err != nil {
			t.Fatalf("append %s: %v", c.wire, err)
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
	if res.Events[0].KindRaw != "work_completed" || res.Events[2].KindRaw != "work_started" {
		t.Fatalf("events kinds = %v %v %v, want [work_completed, state_transition, work_started] (DESC)",
			res.Events[0].KindRaw, res.Events[1].KindRaw, res.Events[2].KindRaw)
	}
}

func testEventsListAuthPayloadFilters(t *testing.T, d persistence.Database) {
	t.Helper()
	defer d.Close()
	ctx := context.Background()
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := d.Tables()

	keyA := uuid.NewString()
	keyB := uuid.NewString()

	rows := []map[string]any{
		{"key_id": keyA, "key_name": "alpha", "action": "instance:create", "response_status": 201, "mode": "execute", "request_path": "/instances"},
		{"key_id": keyA, "key_name": "alpha", "action": "instance:read", "response_status": 200, "mode": "execute", "request_path": "/instances/abc"},
		{"key_id": keyB, "key_name": "beta", "action": "auth:create", "response_status": 200, "mode": "dry_run", "request_path": "/auth/keys"},
		{"key_id": keyB, "key_name": "beta", "action": "auth:revoke", "response_status": 403, "mode": "execute", "request_path": "/auth/keys/xyz"},
	}
	for _, p := range rows {
		p := p
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			return store.Events().Append(ctx, persistence.EventAppendInput{
				Kind:    events.KindAuthAccessAttempted(),
				Payload: p,
			}, tx)
		}); err != nil {
			t.Fatalf("append auth row: %v", err)
		}
	}

	sp := func(s string) *string { return &s }
	ip := func(i int) *int { return &i }

	list := func(f persistence.EventListFilter) []persistence.EventRow {
		t.Helper()
		var res persistence.EventListResult
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			r, err := store.Events().List(ctx, f, persistence.ListPagination{Limit: 50}, tx)
			res = r
			return err
		}); err != nil {
			t.Fatalf("List: %v", err)
		}
		return res.Events
	}

	kindIn := []string{"auth.access_attempted", "auth.access_denied"}

	if got := list(persistence.EventListFilter{KindIn: kindIn}); len(got) != 4 {
		t.Fatalf("no-filter = %d rows, want 4", len(got))
	}

	if got := list(persistence.EventListFilter{KindIn: kindIn, KeyID: sp(keyA)}); len(got) != 2 {
		t.Fatalf("KeyID = %d rows, want 2", len(got))
	}

	if got := list(persistence.EventListFilter{KindIn: kindIn, KeyName: sp("beta")}); len(got) != 2 {
		t.Fatalf("KeyName = %d rows, want 2", len(got))
	}

	got := list(persistence.EventListFilter{KindIn: kindIn, ActionExact: sp("instance:create")})
	if len(got) != 1 {
		t.Fatalf("ActionExact = %d rows, want 1", len(got))
	}
	if a, _ := got[0].Payload["action"].(string); a != "instance:create" {
		t.Fatalf("ActionExact row action = %q, want instance:create", a)
	}

	if got := list(persistence.EventListFilter{KindIn: kindIn, ActionPrefix: sp("instance:")}); len(got) != 2 {
		t.Fatalf("ActionPrefix instance: = %d rows, want 2", len(got))
	}
	if got := list(persistence.EventListFilter{KindIn: kindIn, ActionPrefix: sp("auth:")}); len(got) != 2 {
		t.Fatalf("ActionPrefix auth: = %d rows, want 2", len(got))
	}

	if got := list(persistence.EventListFilter{KindIn: kindIn, ResponseStatus: ip(200)}); len(got) != 2 {
		t.Fatalf("ResponseStatus 200 = %d rows, want 2", len(got))
	}
	if got := list(persistence.EventListFilter{KindIn: kindIn, ResponseStatus: ip(403)}); len(got) != 1 {
		t.Fatalf("ResponseStatus 403 = %d rows, want 1", len(got))
	}

	if got := list(persistence.EventListFilter{KindIn: kindIn, Mode: sp("dry_run")}); len(got) != 1 {
		t.Fatalf("Mode dry_run = %d rows, want 1", len(got))
	}
	if got := list(persistence.EventListFilter{KindIn: kindIn, Mode: sp("execute")}); len(got) != 3 {
		t.Fatalf("Mode execute = %d rows, want 3", len(got))
	}

	got = list(persistence.EventListFilter{KindIn: kindIn, RequestPath: sp("/instances")})
	if len(got) != 1 {
		t.Fatalf("RequestPath /instances = %d rows, want 1", len(got))
	}
	if a, _ := got[0].Payload["action"].(string); a != "instance:create" {
		t.Fatalf("RequestPath row action = %q, want instance:create", a)
	}
	if got := list(persistence.EventListFilter{KindIn: kindIn, RequestPath: sp("/auth/keys/xyz")}); len(got) != 1 {
		t.Fatalf("RequestPath /auth/keys/xyz = %d rows, want 1", len(got))
	}
	if got := list(persistence.EventListFilter{KindIn: kindIn, RequestPath: sp("/nonexistent")}); len(got) != 0 {
		t.Fatalf("RequestPath /nonexistent = %d rows, want 0", len(got))
	}

	got = list(persistence.EventListFilter{KindIn: kindIn, KeyID: sp(keyB), ResponseStatus: ip(200)})
	if len(got) != 1 {
		t.Fatalf("KeyID(B)+Status(200) = %d rows, want 1", len(got))
	}
	if a, _ := got[0].Payload["action"].(string); a != "auth:create" {
		t.Fatalf("composed-filter row action = %q, want auth:create", a)
	}
}
