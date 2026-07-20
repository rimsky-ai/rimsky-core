// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package conformance

import (
	"context"
	"testing"
	"time"

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

// @concept: template
func testTemplatesListPaginationSurvivesCursorRowDeletion(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	store := d.Tables()

	hashes := make([]string, 3)
	for i := range hashes {
		hashes[i] = "sha256-" + uuid.NewString()
		tmplSpec := spec.TemplateSpec{
			Name:    "conformance-cursor-deletion",
			Version: "1",
			Nodes: []spec.TemplateNodeDef{
				{Type: "fixture-node-type", Executor: "test-executor"},
			},
		}
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return store.Templates().Insert(ctx, persistence.TemplateInsertInput{
				ID: hashes[i], Spec: tmplSpec, State: persistence.TemplateStateRegistered, Source: "direct",
			}, tx)
		}); err != nil {
			t.Fatalf("Insert template %d: %v", i, err)
		}
	}

	var page1 persistence.PaginatedListResult[persistence.TemplateRow]
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		p, err := store.Templates().List(ctx, persistence.TemplateListFilter{}, persistence.ListPagination{Limit: 2}, tx)
		page1 = p
		return err
	}); err != nil {
		t.Fatalf("List page1: %v", err)
	}
	if len(page1.Rows) != 2 || page1.NextCursor == "" {
		t.Fatalf("List page1 = %+v, want 2 rows with a NextCursor", page1)
	}

	cursorRowID := page1.Rows[len(page1.Rows)-1].ID
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.Templates().DeleteByHash(ctx, cursorRowID, tx)
	}); err != nil {
		t.Fatalf("delete cursor row %s: %v", cursorRowID, err)
	}

	var page2 persistence.PaginatedListResult[persistence.TemplateRow]
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		p, err := store.Templates().List(ctx, persistence.TemplateListFilter{},
			persistence.ListPagination{Limit: 50, Cursor: page1.NextCursor}, tx)
		page2 = p
		return err
	}); err != nil {
		t.Fatalf("List page2: %v", err)
	}
	if len(page2.Rows) != 1 {
		t.Fatalf("List page2 after cursor-row deletion = %d rows, want 1 (pagination must not silently truncate)", len(page2.Rows))
	}
}

// @concept: template
func testTemplatesListKeysetCursorTieBreak(
	t *testing.T, d persistence.Database,
	rawExec func(t *testing.T, d persistence.Database, sql string, args ...any),
) {
	ctx := context.Background()
	store := d.Tables()

	const fixedNanosLayout = "2006-01-02T15:04:05.000000000Z07:00"
	tie := time.Now().UTC().Truncate(time.Second).Format(fixedNanosLayout)
	hashes := make([]string, 3)
	for i := range hashes {
		hashes[i] = "sha256-" + uuid.NewString()
		tmplSpec := spec.TemplateSpec{
			Name:    "conformance-cursor-tie",
			Version: "1",
			Nodes: []spec.TemplateNodeDef{
				{Type: "fixture-node-type", Executor: "test-executor"},
			},
		}
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			return store.Templates().Insert(ctx, persistence.TemplateInsertInput{
				ID: hashes[i], Spec: tmplSpec, State: persistence.TemplateStateRegistered, Source: "direct",
			}, tx)
		}); err != nil {
			t.Fatalf("Insert template %d: %v", i, err)
		}
		rawExec(t, d, "UPDATE rimsky_templates SET registered_at = ? WHERE id = ?", tie, hashes[i])
	}

	want := map[string]bool{hashes[0]: true, hashes[1]: true, hashes[2]: true}
	var seen []string
	cursor := ""
	for {
		var page persistence.PaginatedListResult[persistence.TemplateRow]
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			p, err := store.Templates().List(ctx, persistence.TemplateListFilter{},
				persistence.ListPagination{Limit: 1, Cursor: cursor}, tx)
			page = p
			return err
		}); err != nil {
			t.Fatalf("List page after cursor %q: %v", cursor, err)
		}
		if len(page.Rows) == 0 {
			break
		}
		seen = append(seen, page.Rows[0].ID)
		if len(seen) > len(hashes) {
			t.Fatalf("tie-break paging returned more rows than exist (duplicate): %v", seen)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}

	if len(seen) != len(hashes) {
		t.Fatalf("tie-break paging visited %d rows, want %d (seen=%v)", len(seen), len(hashes), seen)
	}
	for _, id := range seen {
		if !want[id] {
			t.Fatalf("tie-break paging visited unexpected id %s", id)
		}
		delete(want, id)
	}
	if len(want) != 0 {
		t.Fatalf("tie-break paging skipped rows at the identical-registered_at tie: %v", want)
	}
}
