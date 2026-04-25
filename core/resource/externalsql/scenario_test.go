package externalsql

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/internal/pgtest"
	"github.com/fallguy/rimsky/core/resource"
)

// TestExternalSQL_Scenario_FullLifecycle walks through the full lifecycle an
// external-sql resource is expected to support in production:
//
//  1. provision (factory probe),
//  2. initial commit (3 rows),
//  3. second commit with partially-different payload (4 rows),
//  4. rollback to previous (swaps back to 3 rows),
//  5. fresh commit after rollback (supersedes restored "current" cleanly).
//
// This mirrors the future test/scenarios/external_sql_rollback_test.go but
// drives the Resource directly — the scenario harness does not yet know how
// to wire external-sql Factories. When Plan C Phase 2 adds harness support,
// the top-level scenarios test can delegate to the same assertions here.
func TestExternalSQL_Scenario_FullLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	reg := newFakeRegistry()
	schema := setupTable(t, ctx, pool)
	res := newResource(t, ctx, pool, reg, schema, nil)

	// Step 1: provisioning succeeded (Factory.Create inside newResource did
	// the probe); just double-check the helper contract.
	require.NotNil(t, res)
	require.Equal(t, []string{"items", schema}, res.Path())

	// Step 2: initial commit, 3 rows.
	v1, err := res.Commit(ctx, resource.CommitRequest{
		ProducedBy: uuid.New(),
		Result: []map[string]any{
			{"id": "a", "name": "Alpha", "category": "R1"},
			{"id": "b", "name": "Beta", "category": "R2"},
			{"id": "c", "name": "Gamma", "category": "C1"},
		},
		Changed:       true,
		ChangeSummary: "initial",
	})
	require.NoError(t, err)
	require.True(t, v1.Accepted)
	require.Equal(t, 3, rowCount(t, ctx, pool, schema, "items"))
	require.True(t, tableExists(t, ctx, pool, schema, "items__staging"))

	// Step 3: second commit, 4 rows.
	v2, err := res.Commit(ctx, resource.CommitRequest{
		ProducedBy: uuid.New(),
		Result: []map[string]any{
			{"id": "a", "name": "Alpha-v2", "category": "R1"},
			{"id": "b", "name": "Beta-v2", "category": "R2"},
			{"id": "c", "name": "Gamma-v2", "category": "C1"},
			{"id": "d", "name": "Delta", "category": "C2"},
		},
		Changed:       true,
		ChangeSummary: "expand",
	})
	require.NoError(t, err)
	require.True(t, v2.Accepted)
	require.Equal(t, 4, rowCount(t, ctx, pool, schema, "items"))
	require.Equal(t, 3, rowCount(t, ctx, pool, schema, "items__previous"))

	// Registry pointers tracked the double buffer. Look up the resource row
	// by owner — the fake registry keys on node-id.
	owned, err := reg.ListByOwner(ctx, res.OwnerNodeID(), nil)
	require.NoError(t, err)
	require.Len(t, owned, 1)
	require.NotNil(t, owned[0].CurrentVersionID)
	require.NotNil(t, owned[0].PreviousVersionID)
	require.Equal(t, v2.Version.ID, *owned[0].CurrentVersionID)
	require.Equal(t, v1.Version.ID, *owned[0].PreviousVersionID)

	// Step 4: rollback to previous.
	restored, err := res.RestoreVersion(ctx, resource.VersionRef{Kind: "previous"})
	require.NoError(t, err)
	require.NotNil(t, restored)
	require.Equal(t, 3, rowCount(t, ctx, pool, schema, "items"))
	require.Equal(t, 4, rowCount(t, ctx, pool, schema, "items__previous"))

	var name string
	err = pool.QueryRow(ctx, fmt.Sprintf(`SELECT name FROM %q.items WHERE id='a'`, schema)).Scan(&name)
	require.NoError(t, err)
	require.Equal(t, "Alpha", name, "rollback should restore v1 content, not v2")

	// Step 5: fresh commit after rollback. Should succeed and produce yet
	// another version; the resource is back on the happy path.
	v3, err := res.Commit(ctx, resource.CommitRequest{
		ProducedBy: uuid.New(),
		Result: []map[string]any{
			{"id": "x", "name": "Xray", "category": "Z"},
			{"id": "y", "name": "Yankee", "category": "Z"},
		},
		Changed:       true,
		ChangeSummary: "post-rollback",
	})
	require.NoError(t, err)
	require.True(t, v3.Accepted)
	require.Equal(t, 2, rowCount(t, ctx, pool, schema, "items"))
}
