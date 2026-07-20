// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: instance
package conformance

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func testInstancesCreateConflictErrorsDistinguishIDFromKey(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	store := d.Tables()

	templateHash := "sha256-" + uuid.NewString()
	tmpl := spec.TemplateSpec{
		Name: "instance-conflict-fixture", Version: "1",
		Nodes: []spec.TemplateNodeDef{{Type: "fixture-node-type", Executor: "test-executor"}},
	}
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: templateHash, Spec: tmpl, State: persistence.TemplateStateRegistered, Source: "direct",
		}, tx)
	}); err != nil {
		t.Fatalf("seed template: %v", err)
	}

	firstID := shared.UUID(uuid.New())
	sharedKey := "shared-key"
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		_, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: firstID, TemplateHash: templateHash, InstanceKey: &sharedKey,
		}, tx)
		return err
	}); err != nil {
		t.Fatalf("create first instance: %v", err)
	}

	differentKey := "different-key"
	err := inTx(ctx, store, func(tx persistence.Tx) error {
		_, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: firstID, TemplateHash: templateHash, InstanceKey: &differentKey,
		}, tx)
		return err
	})
	if err == nil {
		t.Fatalf("duplicate instance id must error")
	}
	if errors.Is(err, shared.ErrInstanceKeyConflict) {
		t.Fatalf("duplicate instance id misreported as ErrInstanceKeyConflict: %v", err)
	}

	secondID := shared.UUID(uuid.New())
	err = inTx(ctx, store, func(tx persistence.Tx) error {
		_, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{
			ID: secondID, TemplateHash: templateHash, InstanceKey: &sharedKey,
		}, tx)
		return err
	})
	if !errors.Is(err, shared.ErrInstanceKeyConflict) {
		t.Fatalf("duplicate instance_key = %v, want ErrInstanceKeyConflict", err)
	}
}

func testInstancesListPaginationSurvivesCursorRowDeletion(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	store := d.Tables()

	templateHash := "sha256-" + uuid.NewString()
	tmpl := spec.TemplateSpec{
		Name: "instance-cursor-deletion-fixture", Version: "1",
		Nodes: []spec.TemplateNodeDef{{Type: "fixture-node-type", Executor: "test-executor"}},
	}
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: templateHash, Spec: tmpl, State: persistence.TemplateStateRegistered, Source: "direct",
		}, tx)
	}); err != nil {
		t.Fatalf("seed template: %v", err)
	}

	ids := make([]shared.UUID, 3)
	for i := range ids {
		ids[i] = shared.UUID(uuid.New())
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			_, err := store.Instances().Create(ctx, persistence.InstanceCreateInput{
				ID: ids[i], TemplateHash: templateHash,
			}, tx)
			return err
		}); err != nil {
			t.Fatalf("create instance %d: %v", i, err)
		}
	}

	var page1 persistence.PaginatedListResult[persistence.InstanceRow]
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		p, err := store.Instances().List(ctx,
			persistence.InstanceListFilter{TemplateHash: templateHash},
			persistence.ListPagination{Limit: 2}, tx)
		page1 = p
		return err
	}); err != nil {
		t.Fatalf("List page1: %v", err)
	}
	if len(page1.Rows) != 2 || page1.NextCursor == "" {
		t.Fatalf("List page1 = %+v, want 2 rows with a NextCursor", page1)
	}

	cursorRowID := page1.Rows[len(page1.Rows)-1].ID
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.Instances().Delete(ctx, cursorRowID, tx)
	}); err != nil {
		t.Fatalf("delete cursor row %s: %v", cursorRowID, err)
	}

	var page2 persistence.PaginatedListResult[persistence.InstanceRow]
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		p, err := store.Instances().List(ctx,
			persistence.InstanceListFilter{TemplateHash: templateHash},
			persistence.ListPagination{Limit: 2, Cursor: page1.NextCursor}, tx)
		page2 = p
		return err
	}); err != nil {
		t.Fatalf("List page2: %v", err)
	}
	if len(page2.Rows) != 1 {
		t.Fatalf("List page2 after cursor-row deletion = %d rows, want 1 (pagination must not silently truncate)", len(page2.Rows))
	}
}
