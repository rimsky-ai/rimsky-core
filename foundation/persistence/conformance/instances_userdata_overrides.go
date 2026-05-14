// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package conformance

import (
	"context"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/foundation/spec"
)

// testInstancesUserdataOverridesRoundTrip verifies that
// rimsky_instances.userdata_overrides round-trips through Create + Get
// on every driver. The shape is opaque to rimsky, so the test asserts
// byte-equivalence (after JSON unmarshal canonicalisation) of the
// nested-map payload.
func testInstancesUserdataOverridesRoundTrip(t *testing.T, d persistence.Database) {
	t.Helper()
	defer d.Close()
	ctx := context.Background()
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := d.Tables()

	tmpl := "sha256-" + uuid.NewString()
	id := uuid.New()
	overrides := map[string]any{
		"by_executor": map[string]any{
			"claude-agent": map[string]any{
				"cli": map[string]any{
					"synthetic_scenario": "exit-clean-no-callback",
					"trace_to":           "/var/traces/run.jsonl",
				},
			},
		},
		"by_node": map[string]any{
			"area-pass": map[string]any{
				"cli": map[string]any{"trace_to": "/var/traces/area-pass.jsonl"},
			},
		},
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if err := store.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: tmpl,
			Spec: spec.TemplateSpec{
				Name: "userdata-overrides", Version: "1",
				FrameResolutionMode: spec.FrameResolutionSerialQueue,
				FrameTimeoutMs:      600000,
				Nodes:               []spec.TemplateNodeDef{{Type: "n", Executor: "e"}},
			},
			State:  persistence.TemplateStateRegistered,
			Source: "direct",
		}, tx); err != nil {
			return err
		}
		_, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID:                id,
			TemplateHash:      tmpl,
			Params:            map[string]any{},
			UserdataOverrides: overrides,
		}, tx)
		return err
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	var got *persistence.InstanceRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.Instances().Get(ctx, id, tx)
		got = r
		return err
	}); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatalf("Get returned nil")
	}
	if !reflect.DeepEqual(got.UserdataOverrides, overrides) {
		t.Fatalf("UserdataOverrides round-trip mismatch:\n got = %#v\nwant = %#v", got.UserdataOverrides, overrides)
	}
}

// testInstancesUserdataOverridesMigrationBackfill verifies that
// rimsky_instances rows inserted WITHOUT specifying the
// userdata_overrides column receive the column's DEFAULT '{}' (mirrors
// the case where a pre-existing row was retroactively backfilled by
// the migration that ALTERed the table to add the column NOT NULL
// DEFAULT '{}'). Both drivers exercise.
//
// Uses a raw INSERT that omits the column rather than going through
// `Instances().Create` (which always supplies `{}` if the input is
// nil) so the test exercises the column DEFAULT, not the application-
// layer default. The raw INSERT is dispatched to a driver-specific
// helper provided by the test harness (drivers do not expose raw SQL
// via the persistence.Database interface).
func testInstancesUserdataOverridesMigrationBackfill(
	t *testing.T,
	d persistence.Database,
	rawExec func(t *testing.T, d persistence.Database, sql string, args ...any),
) {
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
		return store.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: tmpl,
			Spec: spec.TemplateSpec{
				Name: "userdata-overrides-backfill", Version: "1",
				FrameResolutionMode: spec.FrameResolutionSerialQueue,
				FrameTimeoutMs:      600000,
				Nodes:               []spec.TemplateNodeDef{{Type: "n", Executor: "e"}},
			},
			State:  persistence.TemplateStateRegistered,
			Source: "direct",
		}, tx)
	}); err != nil {
		t.Fatalf("template insert: %v", err)
	}

	// Raw INSERT omitting the userdata_overrides column. Mirrors a
	// row that was created before the migration ran. Postgres will
	// fill in DEFAULT '{}'::jsonb at INSERT time; SQLite will fill in
	// '{}' at INSERT time. Either way the round-trip Get below should
	// yield an empty map, not nil and not a JSON-decode error.
	//
	// id is passed as a string so both drivers (postgres UUID column,
	// sqlite TEXT column) accept the value uniformly — Postgres pgx
	// implicitly casts a uuid-shaped string to UUID.
	rawExec(t, d,
		`INSERT INTO rimsky_instances (id, template_hash, instance_key, params)
		 VALUES (?, ?, NULL, ?)`,
		id.String(), tmpl, "{}",
	)

	var got *persistence.InstanceRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.Instances().Get(ctx, id, tx)
		got = r
		return err
	}); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatalf("Get returned nil")
	}
	if got.UserdataOverrides == nil {
		t.Fatalf("UserdataOverrides is nil after migration backfill; want empty map")
	}
	if len(got.UserdataOverrides) != 0 {
		t.Fatalf("UserdataOverrides = %#v, want empty map after migration backfill", got.UserdataOverrides)
	}
}

// testInstancesUserdataOverridesDefaultsEmpty verifies that omitting
// UserdataOverrides on Create persists as an empty map (not nil), so
// dispatch-time reads can deep-merge unconditionally.
func testInstancesUserdataOverridesDefaultsEmpty(t *testing.T, d persistence.Database) {
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
				Name: "userdata-overrides-default", Version: "1",
				FrameResolutionMode: spec.FrameResolutionSerialQueue,
				FrameTimeoutMs:      600000,
				Nodes:               []spec.TemplateNodeDef{{Type: "n", Executor: "e"}},
			},
			State:  persistence.TemplateStateRegistered,
			Source: "direct",
		}, tx); err != nil {
			return err
		}
		_, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID:           id,
			TemplateHash: tmpl,
			Params:       map[string]any{},
			// UserdataOverrides intentionally omitted.
		}, tx)
		return err
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	var got *persistence.InstanceRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.Instances().Get(ctx, id, tx)
		got = r
		return err
	}); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatalf("Get returned nil")
	}
	if got.UserdataOverrides == nil {
		t.Fatalf("UserdataOverrides is nil; want empty map")
	}
	if len(got.UserdataOverrides) != 0 {
		t.Fatalf("UserdataOverrides = %#v, want empty", got.UserdataOverrides)
	}
}
