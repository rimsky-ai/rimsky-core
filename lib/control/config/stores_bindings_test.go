// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package config

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	// @constraint: installs the sqlite driver via init().
	_ "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/sqlite"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

// openMigratedSQLite returns a 1-conn SQLite-backed Tables handle with
// migrations applied, closed on cleanup.
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

// TestLookupInstanceBindings_UnknownInstanceNoPanic reads through
// persistence.Tables.Get, which panics on a nil tx under the option-C
// contract. This is a regression guard for a late-bind resolution path
// that passed nil tx — under both drivers that panicked the executor
// resolver mid-dispatch (it surfaced historically as the all-in-one read
// path wedging). A lookup of an unknown instance is enough to exercise
// the broken line: the read runs (and would panic) before the row is
// ever examined.
func TestLookupInstanceBindings_UnknownInstanceNoPanic(t *testing.T) {
	store := openMigratedSQLite(t).Tables()
	bindings, ok, err := lookupInstanceBindings(context.Background(), store, uuid.NewString())
	if err != nil {
		t.Fatalf("lookupInstanceBindings: unexpected error: %v", err)
	}
	if ok || bindings != nil {
		t.Fatalf("unknown instance: want (nil, false), got (%v, %v)", bindings, ok)
	}
}

// TestLookupInstanceBindings_ReturnsServiceBindings — happy path returns
// the parsed service_bindings map for an instance that carries them.
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
		if err := store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
			ID:         mainRunScopeID,
			GraphName:  spec.MainGraphName,
			InstanceID: instID,
		}); err != nil {
			return err
		}
		_, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID:              instID,
			TemplateHash:    templateHash,
			MainRunScopeID:  mainRunScopeID,
			ServiceBindings: bindingsJSON,
		}, tx)
		return err
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	bindings, ok, err := lookupInstanceBindings(ctx, store, instID.String())
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
