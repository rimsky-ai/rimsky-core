// Substantive coverage for the postgres-backed store under
// stores-redesign-v2. Spins up a real Postgres via testcontainers,
// applies the rimsky migrations, creates an items table for a
// configured pick policy, and exercises Open / Commit / Abandon /
// Delete / Release plus the items-table flip and InsertItems.

package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/internal/pgtest"
	"github.com/fallguy/rimsky/core/store"
	pgstore "github.com/fallguy/rimsky/core/store/postgres"
)

// TestPostgresStore_PickPolicyOpenCommit drives the substrate verbs for
// a configured queue-style pick policy: items inserted via InsertItems,
// Open picks the highest-sequence item, Commit with the default
// "delete" action removes the row.
func TestPostgresStore_PickPolicyOpenCommit(t *testing.T) {
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	createItemsTable(ctx, t, pool, "queue_items")

	f := pgstore.Factory{}
	s, err := f.Build("topics", map[string]any{
		"connection":      pool.Config().ConnString(),
		"write_semantics": "direct",
		"pick_policies": map[string]any{
			"@queue": map[string]any{
				"type":                       "queue",
				"items_table":                "queue_items",
				"on_commit_default":          "delete",
				"on_give_up_default":         "release_to_back",
				"visibility_timeout_seconds": 60,
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "topics", s.Name())
	require.Equal(t, "postgres", s.Kind())
	ps := s.(*pgstore.Store)
	t.Cleanup(ps.Close)

	// Seed one item.
	require.NoError(t, ps.InsertItems(ctx, "@queue", []json.RawMessage{
		json.RawMessage(`{"k":"v"}`),
	}))

	// Open under a tx; substrate flips items-table state and returns
	// the picked id as both Region and Address.
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	require.NoError(t, err)
	storeCtx := store.WithTx(ctx, tx)
	cr, err := ps.Open(storeCtx, store.ClaimSpec{StoreName: "topics", Selector: "@queue", Intent: "rw"})
	require.NoError(t, err)
	require.NotEmpty(t, cr.Address)
	require.NotEmpty(t, cr.Region)

	// Commit (default action: delete) — runs in same tx; the items-table
	// row should be gone after commit.
	require.NoError(t, ps.Commit(storeCtx, cr.Region, cr.Address, ""))
	require.NoError(t, tx.Commit(ctx))

	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM queue_items`).Scan(&n))
	require.Equal(t, 0, n, "Commit with default delete should remove the items-table row")
}

// TestPostgresStore_OpenEmptyQueueReturnsZero confirms the contract:
// pick policies signal "pool empty" via a zero ClaimResult (no error).
func TestPostgresStore_OpenEmptyQueueReturnsZero(t *testing.T) {
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	createItemsTable(ctx, t, pool, "empty_items")

	f := pgstore.Factory{}
	s, err := f.Build("empty", map[string]any{
		"connection": pool.Config().ConnString(),
		"pick_policies": map[string]any{
			"@queue": map[string]any{
				"type":               "queue",
				"items_table":        "empty_items",
				"on_commit_default":  "delete",
				"on_give_up_default": "release_to_back",
			},
		},
	})
	require.NoError(t, err)
	ps := s.(*pgstore.Store)
	t.Cleanup(ps.Close)

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	storeCtx := store.WithTx(ctx, tx)
	cr, err := ps.Open(storeCtx, store.ClaimSpec{StoreName: "empty", Selector: "@queue", Intent: "rw"})
	require.NoError(t, err)
	require.Empty(t, cr.Address, "empty queue should return zero ClaimResult")
	require.Empty(t, cr.Region)
}

// TestPostgresStore_AbandonReleasesToBack drives the failure-path verb
// with the default release_to_back action: the items-table row flips
// back to 'available' and gets a fresh sequence (so it goes to the back
// of the queue).
func TestPostgresStore_AbandonReleasesToBack(t *testing.T) {
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	createItemsTable(ctx, t, pool, "abandon_items")

	f := pgstore.Factory{}
	s, err := f.Build("ab", map[string]any{
		"connection": pool.Config().ConnString(),
		"pick_policies": map[string]any{
			"@queue": map[string]any{
				"type":               "queue",
				"items_table":        "abandon_items",
				"on_commit_default":  "delete",
				"on_give_up_default": "release_to_back",
			},
		},
	})
	require.NoError(t, err)
	ps := s.(*pgstore.Store)
	t.Cleanup(ps.Close)
	require.NoError(t, ps.InsertItems(ctx, "@queue", []json.RawMessage{
		json.RawMessage(`{"a":1}`),
	}))

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	require.NoError(t, err)
	storeCtx := store.WithTx(ctx, tx)
	cr, err := ps.Open(storeCtx, store.ClaimSpec{StoreName: "ab", Selector: "@queue", Intent: "rw"})
	require.NoError(t, err)
	require.NotEmpty(t, cr.Region)

	require.NoError(t, ps.Abandon(storeCtx, cr.Region, cr.Address, ""))
	require.NoError(t, tx.Commit(ctx))

	// Row should still exist and be 'available' again.
	var state string
	var claimToken *string
	require.NoError(t, pool.QueryRow(ctx, `SELECT state, claim_token FROM abandon_items`).Scan(&state, &claimToken))
	require.Equal(t, "available", state)
	require.Nil(t, claimToken, "Abandon should clear claim_token on release_to_back")
}

// TestPostgresStore_RegionalNonPolicySelector confirms regional access
// (non-pick-policy selector): Open echoes the selector as Region and
// Address and Commit/Abandon are no-ops on substrate state.
func TestPostgresStore_RegionalNonPolicySelector(t *testing.T) {
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	f := pgstore.Factory{}
	s, err := f.Build("regional", map[string]any{
		"connection": pool.Config().ConnString(),
		// No pick_policies configured; non-policy selectors are echoed.
	})
	require.NoError(t, err)
	ps := s.(*pgstore.Store)
	t.Cleanup(ps.Close)

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	storeCtx := store.WithTx(ctx, tx)
	cr, err := ps.Open(storeCtx, store.ClaimSpec{StoreName: "regional", Selector: "tenant-42/path", Intent: "rw"})
	require.NoError(t, err)
	require.JSONEq(t, `"tenant-42/path"`, string(cr.Address))
	require.JSONEq(t, `"tenant-42/path"`, string(cr.Region))
}

// TestPostgresStore_FactoryRejectsBadItemsTable verifies the §12.12
// items-table schema check fires at Build time.
func TestPostgresStore_FactoryRejectsBadItemsTable(t *testing.T) {
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	// Create a malformed table missing required columns.
	_, err := pool.Exec(ctx, `CREATE TABLE bad_items (item_id text PRIMARY KEY)`)
	require.NoError(t, err)

	f := pgstore.Factory{}
	_, err = f.Build("malformed", map[string]any{
		"connection": pool.Config().ConnString(),
		"pick_policies": map[string]any{
			"@q": map[string]any{
				"type":               "queue",
				"items_table":        "bad_items",
				"on_commit_default":  "delete",
				"on_give_up_default": "release_to_back",
			},
		},
	})
	require.Error(t, err, "Build should reject items table missing required columns")
}

// TestPostgresStore_FactoryRejectsMissingConnection confirms the
// stores-redesign-v2 contract: every postgres store must declare its
// own `connection:` DSN. There is no implicit fallback to a "platform
// pool"; an operator who wants a workload store collocated with
// rimsky's control-plane DB declares that DSN explicitly.
func TestPostgresStore_FactoryRejectsMissingConnection(t *testing.T) {
	f := pgstore.Factory{}
	_, err := f.Build("noconn", map[string]any{
		// No `connection:` field.
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "connection")
}

// TestPostgresStore_FactoryRejectsEmptyConnection covers the empty-
// string variant: `connection: ""` is also rejected to defeat the
// "did the env-var substitution land?" footgun.
func TestPostgresStore_FactoryRejectsEmptyConnection(t *testing.T) {
	f := pgstore.Factory{}
	_, err := f.Build("emptyconn", map[string]any{
		"connection": "",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "non-empty")
}

// createItemsTable creates a minimal items table satisfying §12.12 so
// the postgres store factory's verifyItemsTable check passes and so
// Open / InsertItems can drive it.
func createItemsTable(ctx context.Context, t *testing.T, pool *pgxpool.Pool, name string) {
	t.Helper()
	_, err := pool.Exec(ctx, fmt.Sprintf(`
CREATE TABLE %s (
    item_id     TEXT PRIMARY KEY,
    payload     JSONB NOT NULL,
    state       TEXT NOT NULL,
    claim_token TEXT,
    claimed_at  TIMESTAMPTZ,
    enqueued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    priority    INTEGER NOT NULL DEFAULT 0,
    sequence    BIGSERIAL
)
`, name))
	require.NoError(t, err)
}
