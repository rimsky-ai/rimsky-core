// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: tag

package conformance

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func testTemplateTagsInsertIfAbsent(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	store := d.Tables()
	hashA := seedConformanceTemplate(ctx, t, d)
	hashB := seedConformanceTemplate(ctx, t, d)
	tag := "conformance-tag-" + uuid.NewString()

	var inserted bool
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		ok, err := store.TemplateTags().InsertIfAbsent(ctx, tag, hashA, tx)
		inserted = ok
		return err
	}); err != nil {
		t.Fatalf("InsertIfAbsent (first): %v", err)
	}
	if !inserted {
		t.Fatalf("first InsertIfAbsent on a fresh tag must succeed")
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		ok, err := store.TemplateTags().InsertIfAbsent(ctx, tag, hashB, tx)
		inserted = ok
		return err
	}); err != nil {
		t.Fatalf("InsertIfAbsent (second): %v", err)
	}
	if inserted {
		t.Fatalf("InsertIfAbsent on an already-existing tag must report inserted=false, not silently move it")
	}

	var row *persistence.TemplateTagRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.TemplateTags().Get(ctx, tag, tx)
		row = r
		return err
	}); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row == nil || row.TemplateID != hashA {
		t.Fatalf("a rejected InsertIfAbsent must leave the tag pointing at the original template; got %+v want %s", row, hashA)
	}
}

func testTemplateTagsUpdateIfExists(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	store := d.Tables()
	hashA := seedConformanceTemplate(ctx, t, d)
	hashB := seedConformanceTemplate(ctx, t, d)
	tag := "conformance-tag-" + uuid.NewString()

	var updated bool
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		ok, err := store.TemplateTags().UpdateIfExists(ctx, tag, hashA, tx)
		updated = ok
		return err
	}); err != nil {
		t.Fatalf("UpdateIfExists (missing tag): %v", err)
	}
	if updated {
		t.Fatalf("UpdateIfExists on a tag that does not exist must report updated=false, not create it")
	}

	if _, err := func() (bool, error) {
		var ok bool
		err := inTx(ctx, store, func(tx persistence.Tx) error {
			var ierr error
			ok, ierr = store.TemplateTags().InsertIfAbsent(ctx, tag, hashA, tx)
			return ierr
		})
		return ok, err
	}(); err != nil {
		t.Fatalf("seed InsertIfAbsent: %v", err)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		ok, err := store.TemplateTags().UpdateIfExists(ctx, tag, hashB, tx)
		updated = ok
		return err
	}); err != nil {
		t.Fatalf("UpdateIfExists (existing tag): %v", err)
	}
	if !updated {
		t.Fatalf("UpdateIfExists on an existing tag must succeed")
	}

	var row *persistence.TemplateTagRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.TemplateTags().Get(ctx, tag, tx)
		row = r
		return err
	}); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row == nil || row.TemplateID != hashB {
		t.Fatalf("UpdateIfExists must repoint the tag; got %+v want %s", row, hashB)
	}
}

func seedConformanceTemplate(ctx context.Context, t *testing.T, d persistence.Database) string {
	t.Helper()
	store := d.Tables()
	hash := "sha256-" + uuid.NewString()
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID: hash,
			Spec: spec.TemplateSpec{
				Name:    "conformance-tag-fixture",
				Version: "1",
				Nodes: []spec.TemplateNodeDef{
					{Type: "fixture-node-type", Executor: "test-executor"},
				},
			},
			State:  persistence.TemplateStateRegistered,
			Source: "direct",
		}, tx)
	}); err != nil {
		t.Fatalf("seedConformanceTemplate: %v", err)
	}
	return hash
}
