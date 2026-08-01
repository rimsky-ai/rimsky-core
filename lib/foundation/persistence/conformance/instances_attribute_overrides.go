// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package conformance

import (
	"context"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func testInstancesAttributeOverridesRoundTrip(t *testing.T, d persistence.Database) {
	ctx := context.Background()
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
				Nodes: []spec.TemplateNodeDef{{Type: "n", Executor: "e"}},
			},
			State:  persistence.TemplateStateRegistered,
			Source: "direct",
		}, tx); err != nil {
			return err
		}
		_ = seedMainRunScopeForInstance(ctx, t, store, id, tx)
		_, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{TargetRoutingIdentity: "test-agent",
			ID:                 id,
			TemplateHash:       tmpl,
			Params:             map[string]any{},
			AttributeOverrides: overrides,
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

func testInstancesAttributeOverridesDefaultsEmpty(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	store := d.Tables()

	tmpl := "sha256-" + uuid.NewString()
	id := uuid.New()
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if err := store.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: tmpl,
			Spec: spec.TemplateSpec{
				Name: "attribute-overrides-default", Version: "1",
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
			Params:       map[string]any{},
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
