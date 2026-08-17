// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package conformance

import (
	"context"
	"errors"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// @concept: template
func testTemplateDeleteRefusedWhileATerminatedInstanceReferencesIt(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.Instances().MarkTerminated(ctx, fix.InstanceID, tx)
	}); err != nil {
		t.Fatalf("MarkTerminated: %v", err)
	}

	err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.Templates().DeleteByHash(ctx, fix.TemplateHash, tx)
	})
	if !errors.Is(err, shared.ErrTemplateInUse) {
		t.Fatalf("DeleteByHash while a terminated instance still references the template = %v, "+
			"want ErrTemplateInUse: the restrict-on-delete reference is a caller conflict, not a store fault, "+
			"so it reaches the template-in-use error and its 409 rather than escaping as an internal error", err)
	}

	var row *persistence.TemplateRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, gerr := store.Templates().GetByHash(ctx, fix.TemplateHash, tx)
		row = r
		return gerr
	}); err != nil {
		t.Fatalf("GetByHash after refused delete: %v", err)
	}
	if row == nil {
		t.Fatalf("the refused delete removed the template anyway")
	}
}
