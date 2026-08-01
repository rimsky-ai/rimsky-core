// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

func testTemplateTagsListDeleteCountRoundTrip(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	store := d.Tables()
	hashA := seedConformanceTemplate(ctx, t, d)
	hashB := seedConformanceTemplate(ctx, t, d)

	suffix := uuid.NewString()
	tagA1 := "conformance-list-a1-" + suffix
	tagA2 := "conformance-list-a2-" + suffix
	tagB1 := "conformance-list-b1-" + suffix

	for _, tc := range []struct {
		tag        string
		templateID string
	}{
		{tagA1, hashA}, {tagA2, hashA}, {tagB1, hashB},
	} {
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			_, err := store.TemplateTags().InsertIfAbsent(ctx, tc.tag, tc.templateID, tx)
			return err
		}); err != nil {
			t.Fatalf("seed InsertIfAbsent(%s): %v", tc.tag, err)
		}
	}

	var byTemplateA []persistence.TemplateTagRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		rows, err := store.TemplateTags().ListByTemplate(ctx, hashA, tx)
		byTemplateA = rows
		return err
	}); err != nil {
		t.Fatalf("ListByTemplate(hashA): %v", err)
	}
	if len(byTemplateA) != 2 || byTemplateA[0].Tag != tagA1 || byTemplateA[1].Tag != tagA2 {
		t.Fatalf("ListByTemplate(hashA) = %+v, want [%s, %s] in tag-ascending order", byTemplateA, tagA1, tagA2)
	}

	var countA, countB int
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		n, err := store.TemplateTags().CountByTemplate(ctx, hashA, tx)
		countA = n
		return err
	}); err != nil {
		t.Fatalf("CountByTemplate(hashA): %v", err)
	}
	if countA != 2 {
		t.Fatalf("CountByTemplate(hashA) = %d, want 2", countA)
	}
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		n, err := store.TemplateTags().CountByTemplate(ctx, hashB, tx)
		countB = n
		return err
	}); err != nil {
		t.Fatalf("CountByTemplate(hashB): %v", err)
	}
	if countB != 1 {
		t.Fatalf("CountByTemplate(hashB) = %d, want 1", countB)
	}

	var page persistence.PaginatedListResult[persistence.TemplateTagRow]
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		p, err := store.TemplateTags().List(ctx, persistence.ListPagination{Limit: 1, Cursor: tagA1}, tx)
		page = p
		return err
	}); err != nil {
		t.Fatalf("List(cursor=%s): %v", tagA1, err)
	}
	if len(page.Rows) != 1 || page.Rows[0].Tag != tagA2 {
		t.Fatalf("List(cursor=%s) = %+v, want next tag %s", tagA1, page, tagA2)
	}
	if page.NextCursor != tagA2 {
		t.Fatalf("List(cursor=%s) NextCursor = %q, want %q", tagA1, page.NextCursor, tagA2)
	}

	var deleted bool
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		ok, err := store.TemplateTags().Delete(ctx, tagA1, tx)
		deleted = ok
		return err
	}); err != nil {
		t.Fatalf("Delete(%s): %v", tagA1, err)
	}
	if !deleted {
		t.Fatalf("Delete(%s) on an existing tag must report deleted=true", tagA1)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		ok, err := store.TemplateTags().Delete(ctx, tagA1, tx)
		deleted = ok
		return err
	}); err != nil {
		t.Fatalf("Delete(%s) (already gone): %v", tagA1, err)
	}
	if deleted {
		t.Fatalf("Delete(%s) on an already-deleted tag must report deleted=false, not error", tagA1)
	}

	var row *persistence.TemplateTagRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.TemplateTags().Get(ctx, tagA1, tx)
		row = r
		return err
	}); err != nil {
		t.Fatalf("Get(%s) after delete: %v", tagA1, err)
	}
	if row != nil {
		t.Fatalf("Get(%s) after delete = %+v, want nil", tagA1, row)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		n, err := store.TemplateTags().CountByTemplate(ctx, hashA, tx)
		countA = n
		return err
	}); err != nil {
		t.Fatalf("CountByTemplate(hashA) after delete: %v", err)
	}
	if countA != 1 {
		t.Fatalf("CountByTemplate(hashA) after delete = %d, want 1", countA)
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
