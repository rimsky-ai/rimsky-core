// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package config

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func openMigratedSQLite(t *testing.T) persistence.Database {
	t.Helper()
	dir := t.TempDir()
	d, err := persistence.Open(context.Background(), persistence.Config{
		Driver: "sqlite",
		SQLite: &persistence.SQLiteConfig{Path: filepath.Join(dir, "bindings.db")},
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := d.Migrate(context.Background(), shared.SilentLogger{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return d
}

func TestLookupInstanceBindings_UnknownInstanceNoPanic(t *testing.T) {
	store := openMigratedSQLite(t).Tables()
	bindings, ok, err := LookupInstanceBindings(context.Background(), store, uuid.NewString(), nil)
	if err != nil {
		t.Fatalf("lookupInstanceBindings: unexpected error: %v", err)
	}
	if ok || bindings != nil {
		t.Fatalf("unknown instance: want (nil, false), got (%v, %v)", bindings, ok)
	}
}

func TestLookupInstanceBindings_ReturnsServiceBindings(t *testing.T) {
	store := openMigratedSQLite(t).Tables()
	ctx := context.Background()

	templateHash := "sha256-" + uuid.NewString()
	instID := uuid.New()
	mainRunScopeID := uuid.New()
	bindingsJSON := json.RawMessage(`{"content":"proxy-a:9090"}`)

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID:     templateHash,
			Spec:   spec.TemplateSpec{Name: "fixture", Version: "1.0.0"},
			State:  persistence.TemplateStateRegistered,
			Source: "direct",
		}, tx); err != nil {
			return err
		}
		if err := store.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID:         mainRunScopeID,
			GraphName:  spec.MainGraphName,
			InstanceID: instID,
		}, tx); err != nil {
			return err
		}
		_, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID:                    instID,
			TemplateHash:          templateHash,
			ServiceBindings:       bindingsJSON,
			TargetRoutingIdentity: "test-bindings-daemon",
		}, tx)
		return err
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	bindings, ok, err := LookupInstanceBindings(ctx, store, instID.String(), nil)
	if err != nil {
		t.Fatalf("lookupInstanceBindings: %v", err)
	}
	if !ok {
		t.Fatalf("want ok=true for an instance with service_bindings")
	}
	if got := string(bindings["content"]); got != `"proxy-a:9090"` {
		t.Fatalf("binding mismatch: got %s", got)
	}
}
