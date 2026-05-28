// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// tx.go — TxAtomicity conformance area.
//
// Verifies that Transaction(ctx, fn) rolls back on error and commits on
// nil return — the load-bearing primitive every other test depends on.
package conformance

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

func testTxAtomicity(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	// Roll back: insert a tag inside a tx that returns an error. After
	// return the tag must not exist.
	tagA := "rollback-tag-" + uuid.NewString()
	tryRollback := errors.New("rollback me")
	err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.TemplateTags().Upsert(ctx, tagA, fix.TemplateHash, tx); err != nil {
			return err
		}
		return tryRollback
	})
	if !errors.Is(err, tryRollback) {
		t.Fatalf("expected tryRollback err, got %v", err)
	}
	var got *persistence.TemplateTagRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.TemplateTags().Get(ctx, tagA, tx)
		got = r
		return err
	}); err != nil {
		t.Fatalf("Get tagA: %v", err)
	}
	if got != nil {
		t.Fatalf("rollback failed: tag %q is present after rollback", tagA)
	}

	// Commit: insert two tags in a tx with nil return. Both rows
	// must persist.
	tagB := "commit-tag-b-" + uuid.NewString()
	tagC := "commit-tag-c-" + uuid.NewString()
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.TemplateTags().Upsert(ctx, tagB, fix.TemplateHash, tx); err != nil {
			return err
		}
		if err := store.TemplateTags().Upsert(ctx, tagC, fix.TemplateHash, tx); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("commit tx: %v", err)
	}
	for _, tag := range []string{tagB, tagC} {
		tag := tag
		var row *persistence.TemplateTagRow
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			r, err := store.TemplateTags().Get(ctx, tag, tx)
			row = r
			return err
		}); err != nil {
			t.Fatalf("Get %s: %v", tag, err)
		}
		if row == nil {
			t.Fatalf("commit failed: tag %q is absent", tag)
		}
	}
}
