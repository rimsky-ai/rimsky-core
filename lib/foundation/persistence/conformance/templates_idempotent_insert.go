// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package conformance

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

// @concept: template
func testTemplatesInsertIdempotent(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	store := d.Tables()

	hash := "sha256-" + uuid.NewString()
	tmplSpec := spec.TemplateSpec{
		Name:    "conformance-idempotent-insert",
		Version: "1",
		Nodes: []spec.TemplateNodeDef{
			{Type: "fixture-node-type", Executor: "test-executor"},
		},
	}
	in := persistence.TemplateInsertInput{
		ID:     hash,
		Spec:   tmplSpec,
		State:  persistence.TemplateStateRegistered,
		Source: "direct",
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.Templates().Insert(ctx, in, tx)
	}); err != nil {
		t.Fatalf("first Insert: %v", err)
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.Templates().Insert(ctx, in, tx)
	}); err != nil {
		t.Fatalf("re-registering an identical spec must be a persistence-layer no-op, got error: %v", err)
	}

	var row *persistence.TemplateRow
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var err error
		row, err = store.Templates().GetByHash(ctx, hash, tx)
		return err
	}); err != nil {
		t.Fatalf("GetByHash after duplicate insert: %v", err)
	}
	if row == nil {
		t.Fatalf("GetByHash after duplicate insert: row missing")
	}
	if row.State != persistence.TemplateStateRegistered {
		t.Fatalf("GetByHash after duplicate insert: state = %s, want %s", row.State, persistence.TemplateStateRegistered)
	}
}
