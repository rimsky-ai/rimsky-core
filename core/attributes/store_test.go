// testcontainers-backed CRUD tests for *Store. Each top-level test
// function spins up its own postgres container.
//
// Build: docker socket required.

package attributes

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/internal/pgtest"
	"github.com/fallguy/rimsky/core/shared"
)

// seedNodeFK inserts a minimal template → instance → node chain so the FK
// constraint on rimsky_node_attributes.node_id is satisfied. Returns the
// node ID.
//
// We bypass storage.NodeStore on purpose — it requires deployed templates
// + instances and would couple this Task-9 test against the larger
// storage layer that Task 10/11 are reshaping. Raw SQL is fine: we only
// need the FK target row, not exercising the storage code.
func seedNodeFK(ctx context.Context, t *testing.T, pool *pgxpool.Pool) shared.UUID {
	t.Helper()
	tmplID := uuid.New()
	_, err := pool.Exec(ctx,
		`INSERT INTO rimsky_templates (id, name, version, spec, deployed_at)
		 VALUES ($1, $2, '1', '{}'::jsonb, NOW())`,
		tmplID, "attrs-test-"+tmplID.String()[:8])
	require.NoError(t, err, "seed: insert template")

	instID := uuid.New()
	_, err = pool.Exec(ctx,
		`INSERT INTO rimsky_instances (id, template_id, consumer_key, params, created_at)
		 VALUES ($1, $2, $3, '{}'::jsonb, NOW())`,
		instID, tmplID, "ck-"+instID.String()[:8])
	require.NoError(t, err, "seed: insert instance")

	nodeID := uuid.New()
	_, err = pool.Exec(ctx,
		`INSERT INTO rimsky_nodes
		   (id, instance_id, node_type, state, dependencies, created_at, updated_at)
		 VALUES ($1, $2, 'attrs-test', 'stale', '{}'::uuid[], NOW(), NOW())`,
		nodeID, instID)
	require.NoError(t, err, "seed: insert node")

	return nodeID
}

func TestStore_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	defer teardown()

	nodeID := seedNodeFK(ctx, t, pool)
	store := NewStore(pool)

	// Get on missing row → (nil, nil).
	row, err := store.Get(ctx, nodeID)
	require.NoError(t, err)
	require.Nil(t, row, "row should be absent before first Upsert")

	// Upsert creates the row.
	require.NoError(t, store.Upsert(ctx, nodeID, 0, map[string]any{
		"area":     "northwest",
		"subtopic": "sea-otters",
	}))
	row, err = store.Get(ctx, nodeID)
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, 0, row.RunAttempt)
	require.Equal(t, "northwest", row.Data["area"])
	require.Equal(t, "sea-otters", row.Data["subtopic"])

	// Upsert replaces data outright (executor-populated fields cleared
	// per spec §5.7.3 default — caller decides).
	require.NoError(t, store.Upsert(ctx, nodeID, 1, map[string]any{
		"area": "northwest", // re-populated source-driven field
		// subtopic omitted — Upsert replaces, doesn't merge
	}))
	row, err = store.Get(ctx, nodeID)
	require.NoError(t, err)
	require.Equal(t, 1, row.RunAttempt)
	require.Equal(t, "northwest", row.Data["area"])
	_, hasSubtopic := row.Data["subtopic"]
	require.False(t, hasSubtopic, "Upsert should replace data outright, not merge")

	// MergeDelta merges in additional fields.
	require.NoError(t, store.MergeDelta(ctx, nodeID, map[string]any{
		"scope_notes": "focus on coastal habitats",
		"reviewed":    true,
	}))
	row, err = store.Get(ctx, nodeID)
	require.NoError(t, err)
	require.Equal(t, "northwest", row.Data["area"])
	require.Equal(t, "focus on coastal habitats", row.Data["scope_notes"])
	require.Equal(t, true, row.Data["reviewed"])

	// MergeDelta with overlapping keys overwrites at the top level.
	require.NoError(t, store.MergeDelta(ctx, nodeID, map[string]any{
		"reviewed": false,
		"new_key":  42.0,
	}))
	row, err = store.Get(ctx, nodeID)
	require.NoError(t, err)
	require.Equal(t, false, row.Data["reviewed"])
	require.Equal(t, 42.0, row.Data["new_key"])
	require.Equal(t, "focus on coastal habitats", row.Data["scope_notes"], "non-overlapping keys preserved")
}

func TestStore_MergeDelta_NoRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	defer teardown()

	store := NewStore(pool)
	err := store.MergeDelta(ctx, uuid.New(), map[string]any{"x": 1})
	require.Error(t, err, "merging into a non-existent row should error")
}

func TestStore_MergeDelta_NilDelta(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	defer teardown()

	nodeID := seedNodeFK(ctx, t, pool)
	store := NewStore(pool)
	require.NoError(t, store.Upsert(ctx, nodeID, 0, map[string]any{"keep": "me"}))

	// MergeDelta(nil) is a touch — bumps updated_at, leaves data intact.
	require.NoError(t, store.MergeDelta(ctx, nodeID, nil))
	row, err := store.Get(ctx, nodeID)
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, "me", row.Data["keep"])
}
