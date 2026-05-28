// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// helpers_test.go — small test helpers shared across the scheduler test
// files. Migrated off the retired storage.TemplateTable.Deploy method:
// tests now drive registration through Insert + UpdateState directly
// against persistence.Tables.
package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	nodepkg "github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

// insertDeployedTemplate inserts a template row in 'deployed' state with
// a deterministic content hash derived from name+version. Pre-control-
// plane-v1 these tests called sb.Templates().Deploy(...); the new
// control-plane API splits register from deploy, so tests now drive
// both steps explicitly.
func insertDeployedTemplate(ctx context.Context, t *testing.T, sb persistence.Tables, spec nodepkg.TemplateSpec) persistence.TemplateRow {
	t.Helper()
	hash := deterministicTestHash(spec.Name, spec.Version)
	var row *persistence.TemplateRow
	require.NoError(t, sb.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := sb.Templates().Insert(ctx, persistence.TemplateInsertInput{
			ID:    hash,
			Spec:  spec,
			State: persistence.TemplateStateRegistered,
		}, tx); err != nil {
			return err
		}
		if err := sb.Templates().UpdateState(ctx, hash, persistence.TemplateStateDeployed, tx); err != nil {
			return err
		}
		r, err := sb.Templates().GetByHash(ctx, hash, tx)
		row = r
		return err
	}))
	require.NotNil(t, row)
	return *row
}

func deterministicTestHash(name, version string) string {
	sum := sha256.Sum256([]byte(name + ":" + version))
	return "sha256-" + hex.EncodeToString(sum[:])
}

// inTxTest wraps fn in a fresh Persist.Transaction. Test fixtures and
// helpers run outside any tx; under option C every Store method needs
// an explicit non-nil tx, so reads + writes are wrapped one short tx
// at a time.
func inTxTest(t *testing.T, ctx context.Context, sb persistence.Tables, fn func(tx persistence.Tx) error) {
	t.Helper()
	require.NoError(t, sb.Transaction(ctx, func(_ context.Context, tx persistence.Tx) error {
		return fn(tx)
	}))
}
