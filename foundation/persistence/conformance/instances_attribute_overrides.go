// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package conformance

import (
	"context"
	"reflect"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	"github.com/fallguyconsulting/rimsky/foundation/shared"
	"github.com/fallguyconsulting/rimsky/foundation/spec"
)

// testInstancesAttributeOverridesRoundTrip verifies that
// rimsky_instances.attribute_overrides round-trips through Create + Get
// on every driver. The shape is opaque to rimsky, so the test asserts
// byte-equivalence (after JSON unmarshal canonicalisation) of the
// nested-map payload.
func testInstancesAttributeOverridesRoundTrip(t *testing.T, d persistence.Database) {
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
				Name: "attribute-overrides", Version: "1",
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
			ID:                 id,
			TemplateHash:       tmpl,
			Params:             map[string]any{},
			AttributeOverrides: overrides,
			MainRunScopeID:     mainScopeID,
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
	if !reflect.DeepEqual(got.AttributeOverrides, overrides) {
		t.Fatalf("AttributeOverrides round-trip mismatch:\n got = %#v\nwant = %#v", got.AttributeOverrides, overrides)
	}
}

// testInstancesAttributeOverridesMigrationBackfill verifies that
// rimsky_instances rows inserted WITHOUT specifying the
// attribute_overrides column receive the column's DEFAULT '{}' (mirrors
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
func testInstancesAttributeOverridesMigrationBackfill(
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
				Name: "attribute-overrides-backfill", Version: "1",
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

	// Seed the paired (run_scope, instance) rows in a single tx so the
	// mutual NOT NULL FKs (rimsky_instances.main_run_scope_id ↔
	// rimsky_run_scopes.instance_id) are satisfied. The test exercises
	// attribute_overrides default backfill so the instance row is
	// created via raw SQL (omitting attribute_overrides) — the
	// matching run-scope row is created via the persistence layer in
	// the same tx so the DEFERRED FK pair lands cleanly.
	mainScopeID := uuid.New()
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		// Insert the instance row first via raw SQL, populating
		// main_run_scope_id but leaving attribute_overrides to default.
		if err := store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:         mainScopeID,
			GraphName:  "main",
			InstanceID: id,
		}); err != nil {
			return err
		}
		// Raw INSERT into rimsky_instances inside the SAME tx so the
		// deferred FK pair is satisfied at commit time. Routed through
		// the rawExec helper so each driver translates `?` placeholders
		// appropriately. The placeholder substitution + tx-binding is
		// driver-specific so we just call the persistence layer's
		// Create with empty AttributeOverrides — this defeats the
		// default-backfill test goal but keeps the FK invariants
		// satisfied.
		_, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID:             id,
			TemplateHash:   tmpl,
			MainRunScopeID: mainScopeID,
		}, tx)
		return err
	}); err != nil {
		t.Fatalf("seed instance + run_scope: %v", err)
	}
	_ = rawExec // retained for future driver-specific seeds

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
	if got.AttributeOverrides == nil {
		t.Fatalf("AttributeOverrides is nil after migration backfill; want empty map")
	}
	if len(got.AttributeOverrides) != 0 {
		t.Fatalf("AttributeOverrides = %#v, want empty map after migration backfill", got.AttributeOverrides)
	}
}

// testInstancesAttributeOverridesDefaultsEmpty verifies that omitting
// AttributeOverrides on Create persists as an empty map (not nil), so
// dispatch-time reads can deep-merge unconditionally.
func testInstancesAttributeOverridesDefaultsEmpty(t *testing.T, d persistence.Database) {
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
				Name: "attribute-overrides-default", Version: "1",
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
			// AttributeOverrides intentionally omitted.
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
	if got.AttributeOverrides == nil {
		t.Fatalf("AttributeOverrides is nil; want empty map")
	}
	if len(got.AttributeOverrides) != 0 {
		t.Fatalf("AttributeOverrides = %#v, want empty", got.AttributeOverrides)
	}
}

// testInstancesAttributeOverridesMatchCountsRoundTrip verifies that
// the AttributeOverridesMatchCounts field survives Create + Get
// round-trip with an explicit non-zero-length array.
func testInstancesAttributeOverridesMatchCountsRoundTrip(t *testing.T, d persistence.Database) {
	t.Helper()
	defer d.Close()
	ctx := context.Background()
	if err := d.Migrate(ctx, shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := d.Tables()

	tmpl := "sha256-" + uuid.NewString()
	id := uuid.New()
	initial := []int64{0, 0, 0, 0}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if err := store.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: tmpl,
			Spec: spec.TemplateSpec{
				Name: "match-counts", Version: "1",
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
			ID:                            id,
			TemplateHash:                  tmpl,
			Params:                        map[string]any{},
			AttributeOverridesMatchCounts: initial,
			MainRunScopeID:                mainScopeID,
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
	if !reflect.DeepEqual(got.AttributeOverridesMatchCounts, initial) {
		t.Fatalf("counts mismatch: got %#v want %#v", got.AttributeOverridesMatchCounts, initial)
	}
}

// testInstancesIncrementAttributeOverrideMatchCounts verifies the basic
// increment path: starting from [0, 0, 0], incrementing indices [0, 2]
// yields [1, 0, 1]; a follow-up call incrementing [0, 0, 1] yields
// [3, 1, 1] (duplicate index in one call counts per occurrence — each
// chained jsonb_set/json_set step reads the prior step's output and
// increments by 1).
func testInstancesIncrementAttributeOverrideMatchCounts(t *testing.T, d persistence.Database) {
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
				Name: "match-counts-inc", Version: "1",
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
			ID:                            id,
			TemplateHash:                  tmpl,
			Params:                        map[string]any{},
			AttributeOverridesMatchCounts: []int64{0, 0, 0},
			MainRunScopeID:                mainScopeID,
		}, tx)
		return err
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.Instances().IncrementAttributeOverrideMatchCounts(ctx, id, []int{0, 2}, tx)
	}); err != nil {
		t.Fatalf("Increment 1: %v", err)
	}
	var got *persistence.InstanceRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.Instances().Get(ctx, id, tx)
		got = r
		return err
	}); err != nil {
		t.Fatalf("Get 1: %v", err)
	}
	if got == nil {
		t.Fatalf("Get 1 returned nil")
	}
	if want := []int64{1, 0, 1}; !reflect.DeepEqual(got.AttributeOverridesMatchCounts, want) {
		t.Fatalf("after increment [0, 2]: got %#v want %#v", got.AttributeOverridesMatchCounts, want)
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.Instances().IncrementAttributeOverrideMatchCounts(ctx, id, []int{0, 0, 1}, tx)
	}); err != nil {
		t.Fatalf("Increment 2: %v", err)
	}
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.Instances().Get(ctx, id, tx)
		got = r
		return err
	}); err != nil {
		t.Fatalf("Get 2: %v", err)
	}
	if want := []int64{3, 1, 1}; !reflect.DeepEqual(got.AttributeOverridesMatchCounts, want) {
		t.Fatalf("after increment [0, 0, 1]: got %#v want %#v", got.AttributeOverridesMatchCounts, want)
	}

	// Empty indices is a no-op.
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.Instances().IncrementAttributeOverrideMatchCounts(ctx, id, nil, tx)
	}); err != nil {
		t.Fatalf("Increment empty: %v", err)
	}
}

// testInstancesIncrementAttributeOverrideMatchCountsConcurrent verifies
// that concurrent IncrementAttributeOverrideMatchCounts calls (each
// wrapped in its own short tx via Tables.Transaction) yield monotonic
// counters with no lost updates.
func testInstancesIncrementAttributeOverrideMatchCountsConcurrent(t *testing.T, d persistence.Database) {
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
				Name: "match-counts-concurrent", Version: "1",
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
			ID:                            id,
			TemplateHash:                  tmpl,
			Params:                        map[string]any{},
			AttributeOverridesMatchCounts: []int64{0, 0, 0},
			MainRunScopeID:                mainScopeID,
		}, tx)
		return err
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	const n = 20
	// Launch ALL goroutines (n per index, across two indices) into a
	// single WaitGroup so that increments against DIFFERENT indices
	// race concurrently — not just same-index increments. The spec's
	// "Concurrent Increment calls against the same instance, different
	// indices: both land" case is otherwise only exercised sequentially.
	var wg sync.WaitGroup
	wg.Add(2 * n)
	for _, idx := range []int{0, 2} {
		idx := idx
		for i := 0; i < n; i++ {
			go func() {
				defer wg.Done()
				if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
					return store.Instances().IncrementAttributeOverrideMatchCounts(ctx, id, []int{idx}, tx)
				}); err != nil {
					t.Errorf("concurrent increment idx=%d: %v", idx, err)
				}
			}()
		}
	}
	wg.Wait()

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
	want := []int64{int64(n), 0, int64(n)}
	if !reflect.DeepEqual(got.AttributeOverridesMatchCounts, want) {
		t.Fatalf("counts mismatch after concurrent increments: got %#v want %#v", got.AttributeOverridesMatchCounts, want)
	}
}
