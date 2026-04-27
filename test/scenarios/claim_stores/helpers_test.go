// Shared helpers for the claim_stores scenario suite. Each scenario file
// uses these to spin up a real `core/store/claimstorepg/` store against
// a testcontainers postgres (provisioned by the scenario harness with
// scheduler + supervisor disabled — we only need the pool + migrated
// schema), seed the §9.10 items table, and exercise claim-acquisition +
// release + resolution paths end-to-end.
//
// We deliberately drive the store directly rather than through the full
// scheduler + supervisor pipeline — the §19.1 claim_stores cases are
// about queue and resolution mechanics, not about scheduler/supervisor
// wiring (those are covered by the executor / locks / attributes
// scenario buckets). Driving the store directly keeps the assertions
// precise and avoids needing to pre-create the items table inside the
// harness's BuildAll.
package claim_stores

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/store"
	"github.com/fallguy/rimsky/core/store/claimstorepg"
)

// startPostgres returns a ready *pgxpool.Pool against a testcontainers
// Postgres with rimsky migrations applied. We re-use the scenario
// harness with both background services disabled so the only thing that
// runs is pgtest's container + migration runner. Cleanups are
// registered with t via the harness — no extra teardown needed.
//
// Returning *pgxpool.Pool keeps the test bodies short: every helper in
// this file accepts *pgxpool.Pool, mirroring the shape used by the
// claim-store unit tests in `core/store/claimstorepg/*_test.go`.
func startPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	h := scenario.Start(t, scenario.HarnessOpts{
		NoSupervisor: true,
		NoScheduler:  true,
	})
	return h.Pool
}

// withTxCtx is a thin alias around store.WithTx so tests don't import
// `core/store` just to attach a tx to a context. Callers use it the same
// way the supervisor does inside its release transaction.
func withTxCtx(ctx context.Context, tx pgx.Tx) context.Context {
	return store.WithTx(ctx, tx)
}

// createItemsTable creates the §9.10 schema for a claim-store items table.
// The factory's verify-on-Build step expects exactly this column shape.
func createItemsTable(t *testing.T, pool *pgxpool.Pool, name string) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`CREATE TABLE `+name+` (
			item_id     UUID PRIMARY KEY,
			payload     JSONB NOT NULL,
			enqueued_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			state       TEXT NOT NULL DEFAULT 'available',
			claim_token UUID,
			claimed_at  TIMESTAMPTZ
		)`)
	require.NoError(t, err)
}

// buildStore constructs a *claimstorepg.Store with the given name + items
// table and the supplied on_commit / on_give_up defaults.
func buildStore(
	t *testing.T,
	pool *pgxpool.Pool,
	name, table, onCommit, onGiveUp string,
) *claimstorepg.Store {
	t.Helper()
	s, err := claimstorepg.Factory{Pool: pool}.Build(name, map[string]any{
		"backend":                    "postgres",
		"items_table":                table,
		"on_commit_default":          onCommit,
		"on_give_up_default":         onGiveUp,
		"visibility_timeout_seconds": 300,
	})
	require.NoError(t, err)
	return s.(*claimstorepg.Store)
}

// insertItem inserts one row into the items table with a generated UUID
// and the supplied payload. Returns the item ID. Caller controls
// enqueued_at via the optional `enqAt` (zero = use column default).
func insertItem(
	t *testing.T,
	pool *pgxpool.Pool,
	table string,
	payload map[string]any,
	enqAt time.Time,
) uuid.UUID {
	t.Helper()
	id := uuid.New()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	if enqAt.IsZero() {
		_, err = pool.Exec(context.Background(),
			`INSERT INTO `+table+` (item_id, payload) VALUES ($1, $2::jsonb)`,
			id, raw,
		)
	} else {
		_, err = pool.Exec(context.Background(),
			`INSERT INTO `+table+` (item_id, payload, enqueued_at) VALUES ($1, $2::jsonb, $3)`,
			id, raw, enqAt,
		)
	}
	require.NoError(t, err)
	return id
}

// acquireOnce runs a single AcquireLock inside its own tx, commits, and
// returns the ClaimResult. Mirrors the supervisor's atomic-acquisition
// outer-tx shape (spec §13.3).
func acquireOnce(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	s *claimstorepg.Store,
) store.ClaimResult {
	t.Helper()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, cr, err := s.AcquireLock(store.WithTx(ctx, tx), store.ClaimLockSpec{StoreName: s.Name()})
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
	return cr
}

// readItemState returns (state, claim_token) for the row matching itemID.
// Used by assertions that need to confirm the items-table side of a
// claim/release sequence.
func readItemState(
	t *testing.T,
	pool *pgxpool.Pool,
	table string,
	itemID uuid.UUID,
) (state string, claimToken *uuid.UUID) {
	t.Helper()
	err := pool.QueryRow(context.Background(),
		`SELECT state, claim_token FROM `+table+` WHERE item_id = $1`, itemID,
	).Scan(&state, &claimToken)
	require.NoError(t, err)
	return state, claimToken
}

// countItemsByState returns the number of rows in `table` whose state
// matches `state`. Used to check FIFO drain / ring-buffer-non-deletion
// assertions.
func countItemsByState(
	t *testing.T,
	pool *pgxpool.Pool,
	table, state string,
) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT count(*) FROM `+table+` WHERE state = $1`, state,
	).Scan(&n))
	return n
}

// provisionTemplateInstanceNode creates a (template, instance, node)
// triple in the rimsky_* tables. Returns (template_id, instance_id,
// node_id). Required because rimsky_claim_holders.holder_node_id has an
// FK to rimsky_nodes.id, so any test that exercises ResolveOnTerminal
// needs a real node row.
func provisionTemplateInstanceNode(
	t *testing.T,
	pool *pgxpool.Pool,
	label string,
) (tplID, instID uuid.UUID, nodeID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	tplID = uuid.New()
	_, err := pool.Exec(ctx,
		`INSERT INTO rimsky_templates (id, name, version, spec) VALUES ($1, $2, '1', '{}'::jsonb)`,
		tplID, "tpl-"+label,
	)
	require.NoError(t, err)
	instID = uuid.New()
	_, err = pool.Exec(ctx,
		`INSERT INTO rimsky_instances (id, template_id, consumer_key, params) VALUES ($1, $2, $3, '{}'::jsonb)`,
		instID, tplID, "ck-"+label,
	)
	require.NoError(t, err)
	nodeID = uuid.New()
	_, err = pool.Exec(ctx,
		`INSERT INTO rimsky_nodes (id, instance_id, node_type, state, dependencies) VALUES ($1, $2, $3, 'fresh', '{}')`,
		nodeID, instID, "leaf-"+label,
	)
	require.NoError(t, err)
	return tplID, instID, nodeID
}

// insertSiblingNode adds another node to an existing instance and returns
// its ID. Used by fan-out tests where two terminal-leaf nodes both hold
// the same claim.
func insertSiblingNode(
	t *testing.T,
	pool *pgxpool.Pool,
	instID uuid.UUID,
	label string,
) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO rimsky_nodes (id, instance_id, node_type, state, dependencies) VALUES ($1, $2, $3, 'fresh', '{}')`,
		id, instID, label,
	)
	require.NoError(t, err)
	return id
}

// insertHolder inserts a rimsky_claim_holders row for (claim_id,
// holder_node_id) with the supplied on_commit / on_give_up. Returns the
// row ID.
func insertHolder(
	t *testing.T,
	pool *pgxpool.Pool,
	claimID, storeName string,
	holderNodeID uuid.UUID,
	onCommit, onGiveUp string,
) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO rimsky_claim_holders (id, claim_id, store_name, holder_node_id, on_commit, on_give_up)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		id, claimID, storeName, holderNodeID, onCommit, onGiveUp,
	)
	require.NoError(t, err)
	return id
}

// readHolder returns (state, actual_action) for the holder row matching
// rowID. actual_action is empty when the column is NULL (the default for
// 'active' rows).
func readHolder(
	t *testing.T,
	pool *pgxpool.Pool,
	rowID uuid.UUID,
) (state, actualAction string) {
	t.Helper()
	var action *string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT state, actual_action FROM rimsky_claim_holders WHERE id = $1`, rowID,
	).Scan(&state, &action))
	if action == nil {
		return state, ""
	}
	return state, *action
}

// resolveInTx runs ResolveOnTerminal inside its own tx and commits.
// Mirrors the supervisor's outer release transaction (spec §13.6).
func resolveInTx(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	s *claimstorepg.Store,
	claimID string,
	holderNodeID uuid.UUID,
	outcome claimstorepg.TerminalOutcome,
) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	require.NoError(t, s.ResolveOnTerminal(store.WithTx(ctx, tx), claimID, holderNodeID.String(), outcome))
	require.NoError(t, tx.Commit(ctx))
}

// releaseInTx runs ReleaseClaimItem inside its own tx and commits. Used
// by tests that exercise the claim-and-forget commit path (no held-claim
// row, just the items-table reposition).
func releaseInTx(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	s *claimstorepg.Store,
	claimID, action string,
) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	require.NoError(t, s.ReleaseClaimItem(store.WithTx(ctx, tx), claimID, action))
	require.NoError(t, tx.Commit(ctx))
}

// unsetTime returns the zero time.Time. Pass to insertItem when the
// caller wants the column default for `enqueued_at` (DEFAULT NOW()) to
// apply rather than supplying an explicit timestamp. Sugar for
// readability at call sites.
func unsetTime() time.Time { return time.Time{} }

// mustParseUUID parses s into a uuid.UUID or fails the test on malformed
// input. Used by tests that round-trip claim IDs through the items-table.
func mustParseUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	require.NoError(t, err)
	return id
}

// deleteItemInTx executes the items-table DELETE used by the §5.6.4
// resolution algorithm's delete branch. The non-held commit path issues
// this directly (since `delete` is not a valid ReleaseClaimItem action).
func deleteItemInTx(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	table string,
	claimID string,
) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `DELETE FROM `+table+` WHERE item_id = $1`, claimID)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
}
