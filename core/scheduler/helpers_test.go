// helpers_test.go — small test helpers shared across the scheduler test
// files. Migrated off the retired storage.TemplateStore.Deploy method:
// tests now drive registration through Insert + UpdateState directly.
package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	nodepkg "github.com/fallguy/rimsky/core/node"
	"github.com/fallguy/rimsky/core/storage"
	pgstorage "github.com/fallguy/rimsky/core/storage/postgres"
)

// insertDeployedTemplate inserts a template row in 'deployed' state with
// a deterministic content hash derived from name+version. Pre-control-
// plane-v1 these tests called sb.Templates().Deploy(...); the new
// control-plane API splits register from deploy, so tests now drive
// both steps explicitly.
func insertDeployedTemplate(ctx context.Context, t *testing.T, sb *pgstorage.PostgresStorageBackend, spec nodepkg.TemplateSpec) storage.TemplateRow {
	t.Helper()
	hash := deterministicTestHash(spec.Name, spec.Version)
	require.NoError(t, sb.Templates().Insert(ctx, storage.TemplateInsertInput{
		ID:    hash,
		Spec:  spec,
		State: storage.TemplateStateRegistered,
	}, nil))
	require.NoError(t, sb.Templates().UpdateState(ctx, hash, storage.TemplateStateDeployed, nil))
	row, err := sb.Templates().GetByHash(ctx, hash, nil)
	require.NoError(t, err)
	require.NotNil(t, row)
	return *row
}

func deterministicTestHash(name, version string) string {
	sum := sha256.Sum256([]byte(name + ":" + version))
	return "sha256-" + hex.EncodeToString(sum[:])
}
