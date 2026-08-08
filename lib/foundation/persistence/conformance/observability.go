// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/eventpayload"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func testInstancesFindAnyByInstanceKey(t *testing.T, d persistence.Database) {
	ctx := context.Background()
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
				Nodes: []spec.TemplateNodeDef{{Type: "n", Executor: "e"}},
			},
			State:  persistence.TemplateStateRegistered,
			Source: "direct",
		}, tx); err != nil {
			return err
		}
		_ = seedMainRunScopeForInstance(ctx, t, store, id, tx)
		_, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{TargetRoutingIdentity: "test-agent",
			ID:           id,
			TemplateHash: tmpl,
			InstanceKey:  &key,
			Params:       map[string]any{},
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

func testLifecycleIdempotencyListByClaimProducer(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	store := d.Tables()

	var rows []persistence.LifecycleIdempotencyRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.LifecycleIdempotency().ListByClaimProducer(ctx, "no-claim-producer", tx)
		rows = r
		return err
	}); err != nil {
		t.Fatalf("ListByClaimProducer empty: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("ListByClaimProducer empty returned %d rows", len(rows))
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.LifecycleIdempotency().Upsert(ctx, persistence.LifecycleIdempotencyRow{
			ClaimProducerName: "claim-producer-a",
			ScopeKind:         persistence.LifecycleIdempotencyScopeTemplate,
			ScopeID:           "tpl-1",
			State:             persistence.LifecycleIdempotencyStateRegistered,
		}, tx)
	}); err != nil {
		t.Fatalf("upsert tpl: %v", err)
	}
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.LifecycleIdempotency().Upsert(ctx, persistence.LifecycleIdempotencyRow{
			ClaimProducerName: "claim-producer-a",
			ScopeKind:         persistence.LifecycleIdempotencyScopeInstance,
			ScopeID:           uuid.New().String(),
			State:             persistence.LifecycleIdempotencyStateCreated,
		}, tx)
	}); err != nil {
		t.Fatalf("upsert inst: %v", err)
	}
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.LifecycleIdempotency().ListByClaimProducer(ctx, "claim-producer-a", tx)
		rows = r
		return err
	}); err != nil {
		t.Fatalf("ListByClaimProducer: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("ListByClaimProducer = %d rows, want 2", len(rows))
	}

	explicitLastEventAt := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.LifecycleIdempotency().Upsert(ctx, persistence.LifecycleIdempotencyRow{
			ClaimProducerName: "store-b",
			ScopeKind:         persistence.LifecycleIdempotencyScopeTemplate,
			ScopeID:           "tpl-explicit-last-event",
			State:             persistence.LifecycleIdempotencyStateRegistered,
			LastEventAt:       explicitLastEventAt,
		}, tx)
	}); err != nil {
		t.Fatalf("upsert with explicit LastEventAt: %v", err)
	}
	var got *persistence.LifecycleIdempotencyRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.LifecycleIdempotency().Get(ctx, "store-b", persistence.LifecycleIdempotencyScopeTemplate, "tpl-explicit-last-event", tx)
		got = r
		return err
	}); err != nil {
		t.Fatalf("get after explicit-LastEventAt upsert: %v", err)
	}
	if got == nil {
		t.Fatalf("get after explicit-LastEventAt upsert: row not found")
	}
	if !got.LastEventAt.Equal(explicitLastEventAt) {
		t.Fatalf("Upsert did not honor caller-supplied LastEventAt: got %v, want %v", got.LastEventAt, explicitLastEventAt)
	}
}

func testEventsListDescending(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	store := d.Tables()
	fix := seedFixtureSet(ctx, t, d)
	id := fix.InstanceID

	// @decision: event-log-kind-enum
	cases := []struct {
		kind events.Kind
		wire string
	}{
		{events.KindWorkStarted(), "work_started"},
		{events.KindStateTransition(), "state_transition"},
		{events.KindWorkCompleted(), "work_completed"},
	}
	for _, c := range cases {
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			return store.Events().Append(ctx, persistence.EventAppendInput{
				InstanceID: &id,
				Kind:       c.kind,
				Payload:    eventpayload.New(&genv1.StateTransitionPayload{}),
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
	ctx := context.Background()
	store := d.Tables()

	keyA := uuid.NewString()
	keyB := uuid.NewString()

	rows := []*genv1.AuthAccessAttemptedPayload{
		{KeyId: &keyA, KeyName: "alpha", Action: "instance:create", ResponseStatus: 201, Mode: "execute", RequestPath: "/instances"},
		{KeyId: &keyA, KeyName: "alpha", Action: "instance:read", ResponseStatus: 200, Mode: "execute", RequestPath: "/instances/abc"},
		{KeyId: &keyB, KeyName: "beta", Action: "auth:create", ResponseStatus: 200, Mode: "dry_run", RequestPath: "/auth/keys"},
		{KeyId: &keyB, KeyName: "beta", Action: "auth:revoke", ResponseStatus: 403, Mode: "execute", RequestPath: "/auth/keys/xyz"},
	}
	for _, p := range rows {
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			return store.Events().Append(ctx, persistence.EventAppendInput{
				Kind:    events.KindAuthAccessAttempted(),
				Payload: eventpayload.New(p),
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

	if got := list(persistence.EventListFilter{KindIn: kindIn, AuditPayload: persistence.EventAuditPayloadFilter{KeyID: sp(keyA)}}); len(got) != 2 {
		t.Fatalf("KeyID = %d rows, want 2", len(got))
	}

	if got := list(persistence.EventListFilter{KindIn: kindIn, AuditPayload: persistence.EventAuditPayloadFilter{KeyName: sp("beta")}}); len(got) != 2 {
		t.Fatalf("KeyName = %d rows, want 2", len(got))
	}

	got := list(persistence.EventListFilter{KindIn: kindIn, AuditPayload: persistence.EventAuditPayloadFilter{ActionExact: sp("instance:create")}})
	if len(got) != 1 {
		t.Fatalf("ActionExact = %d rows, want 1", len(got))
	}
	if a, _ := got[0].Payload.Map()["action"].(string); a != "instance:create" {
		t.Fatalf("ActionExact row action = %q, want instance:create", a)
	}

	if got := list(persistence.EventListFilter{KindIn: kindIn, AuditPayload: persistence.EventAuditPayloadFilter{ActionPrefix: sp("instance:")}}); len(got) != 2 {
		t.Fatalf("ActionPrefix instance: = %d rows, want 2", len(got))
	}
	if got := list(persistence.EventListFilter{KindIn: kindIn, AuditPayload: persistence.EventAuditPayloadFilter{ActionPrefix: sp("auth:")}}); len(got) != 2 {
		t.Fatalf("ActionPrefix auth: = %d rows, want 2", len(got))
	}

	if got := list(persistence.EventListFilter{KindIn: kindIn, AuditPayload: persistence.EventAuditPayloadFilter{ResponseStatus: ip(200)}}); len(got) != 2 {
		t.Fatalf("ResponseStatus 200 = %d rows, want 2", len(got))
	}
	if got := list(persistence.EventListFilter{KindIn: kindIn, AuditPayload: persistence.EventAuditPayloadFilter{ResponseStatus: ip(403)}}); len(got) != 1 {
		t.Fatalf("ResponseStatus 403 = %d rows, want 1", len(got))
	}

	if got := list(persistence.EventListFilter{KindIn: kindIn, AuditPayload: persistence.EventAuditPayloadFilter{Mode: sp("dry_run")}}); len(got) != 1 {
		t.Fatalf("Mode dry_run = %d rows, want 1", len(got))
	}
	if got := list(persistence.EventListFilter{KindIn: kindIn, AuditPayload: persistence.EventAuditPayloadFilter{Mode: sp("execute")}}); len(got) != 3 {
		t.Fatalf("Mode execute = %d rows, want 3", len(got))
	}

	got = list(persistence.EventListFilter{KindIn: kindIn, AuditPayload: persistence.EventAuditPayloadFilter{RequestPath: sp("/instances")}})
	if len(got) != 1 {
		t.Fatalf("RequestPath /instances = %d rows, want 1", len(got))
	}
	if a, _ := got[0].Payload.Map()["action"].(string); a != "instance:create" {
		t.Fatalf("RequestPath row action = %q, want instance:create", a)
	}
	if got := list(persistence.EventListFilter{KindIn: kindIn, AuditPayload: persistence.EventAuditPayloadFilter{RequestPath: sp("/auth/keys/xyz")}}); len(got) != 1 {
		t.Fatalf("RequestPath /auth/keys/xyz = %d rows, want 1", len(got))
	}
	if got := list(persistence.EventListFilter{KindIn: kindIn, AuditPayload: persistence.EventAuditPayloadFilter{RequestPath: sp("/nonexistent")}}); len(got) != 0 {
		t.Fatalf("RequestPath /nonexistent = %d rows, want 0", len(got))
	}

	got = list(persistence.EventListFilter{KindIn: kindIn, AuditPayload: persistence.EventAuditPayloadFilter{KeyID: sp(keyB), ResponseStatus: ip(200)}})
	if len(got) != 1 {
		t.Fatalf("KeyID(B)+Status(200) = %d rows, want 1", len(got))
	}
	if a, _ := got[0].Payload.Map()["action"].(string); a != "auth:create" {
		t.Fatalf("composed-filter row action = %q, want auth:create", a)
	}
}

func testEventsListPagination(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	store := d.Tables()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	occurredAt := func(seconds int) *time.Time {
		tm := base.Add(time.Duration(seconds) * time.Second)
		return &tm
	}

	appendEvent := func(at *time.Time) persistence.EventRow {
		var appended persistence.EventRow
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			if err := store.Events().Append(ctx, persistence.EventAppendInput{
				Kind:       events.KindWorkStarted(),
				OccurredAt: at,
			}, tx); err != nil {
				return err
			}
			r, err := store.Events().List(ctx, persistence.EventListFilter{}, persistence.ListPagination{Limit: 1}, tx)
			if err != nil {
				return err
			}
			if len(r.Events) != 1 {
				t.Fatalf("appendEvent: List after append returned %d rows, want 1", len(r.Events))
			}
			appended = r.Events[0]
			return nil
		}); err != nil {
			t.Fatalf("append event: %v", err)
		}
		return appended
	}

	e1 := appendEvent(occurredAt(0))
	e2 := appendEvent(occurredAt(0))
	e3 := appendEvent(occurredAt(1))
	e4 := appendEvent(occurredAt(1))
	e5 := appendEvent(occurredAt(2))

	wantOrder := []int64{e5.ID, e4.ID, e3.ID, e2.ID, e1.ID}

	var full persistence.EventListResult
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.Events().List(ctx, persistence.EventListFilter{}, persistence.ListPagination{Limit: 50}, tx)
		full = r
		return err
	}); err != nil {
		t.Fatalf("List unpaginated: %v", err)
	}
	if len(full.Events) != len(wantOrder) {
		t.Fatalf("unpaginated List: got %d events, want %d", len(full.Events), len(wantOrder))
	}
	for i, want := range wantOrder {
		if full.Events[i].ID != want {
			t.Fatalf("unpaginated List order[%d] = %d, want %d (full order %v)", i, full.Events[i].ID, want, idsOf(full.Events))
		}
	}
	if full.NextCursor != "" {
		t.Fatalf("unpaginated List (fewer than limit): NextCursor = %q, want empty", full.NextCursor)
	}

	var walked []int64
	cursor := ""
	for page := 0; ; page++ {
		if page > len(wantOrder) {
			t.Fatalf("pagination did not terminate; walked so far: %v", walked)
		}
		var res persistence.EventListResult
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			r, err := store.Events().List(ctx, persistence.EventListFilter{},
				persistence.ListPagination{Limit: 2, Cursor: cursor}, tx)
			res = r
			return err
		}); err != nil {
			t.Fatalf("List page %d: %v", page, err)
		}
		for _, e := range res.Events {
			walked = append(walked, e.ID)
		}
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	if len(walked) != len(wantOrder) {
		t.Fatalf("paginated walk visited %d events, want %d; got %v want %v", len(walked), len(wantOrder), walked, wantOrder)
	}
	for i, want := range wantOrder {
		if walked[i] != want {
			t.Fatalf("paginated walk order[%d] = %d, want %d (full walk %v)", i, walked[i], want, walked)
		}
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		_, err := store.Events().List(ctx, persistence.EventListFilter{},
			persistence.ListPagination{Limit: 2, Cursor: "not-valid-base64!!"}, tx)
		return err
	}); err == nil {
		t.Fatalf("List with malformed cursor: want error, got nil")
	}
}

func idsOf(rows []persistence.EventRow) []int64 {
	out := make([]int64, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}
