package claimstorepg

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/internal/pgtest"
	"github.com/fallguy/rimsky/core/store"
)

// TestResolveOnTerminal_LinearDelete covers the simplest case: one
// holder, on_commit=delete. After resolution the holder row is
// 'completed' with actual_action='delete' and the items-table row is
// gone.
func TestResolveOnTerminal_LinearDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	mustCreateItemsTable(t, pool, "linear_items")
	s := mustBuildStore(t, pool, "linear", "linear_items")

	itemID := uuid.New()
	insertItem(ctx, t, pool, "linear_items", itemID, "in_progress")

	templateID, instanceID, holderNodeID := mustProvisionTemplateInstanceNode(ctx, t, pool, "linear")
	defer cleanupTemplateInstance(ctx, t, pool, templateID, instanceID)

	// Insert a single active holder row with on_commit=delete.
	holderRowID := mustInsertHolder(ctx, t, pool, itemID.String(), "linear", holderNodeID, "delete", "release_to_head")

	mustResolve(ctx, t, pool, s, itemID.String(), holderNodeID, TerminalCommit)

	// Holder row: completed, actual_action='delete'.
	state, action := readHolder(ctx, t, pool, holderRowID)
	if state != "completed" {
		t.Fatalf("holder state = %q, want completed", state)
	}
	if action != "delete" {
		t.Fatalf("holder actual_action = %q, want delete", action)
	}

	// Items-table row: gone.
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM linear_items WHERE item_id = $1`, itemID).Scan(&n); err != nil {
		t.Fatalf("count items: %v", err)
	}
	if n != 0 {
		t.Fatalf("linear_items has %d rows for claim_id, want 0 (delete should remove it)", n)
	}
}

// TestResolveOnTerminal_LinearReleaseToBack covers the
// last-released-wins single-holder release path.
func TestResolveOnTerminal_LinearReleaseToBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	mustCreateItemsTable(t, pool, "linear_release_items")
	s := mustBuildStore(t, pool, "linear_release", "linear_release_items")

	itemID := uuid.New()
	insertItem(ctx, t, pool, "linear_release_items", itemID, "in_progress")

	templateID, instanceID, holderNodeID := mustProvisionTemplateInstanceNode(ctx, t, pool, "linear_release")
	defer cleanupTemplateInstance(ctx, t, pool, templateID, instanceID)
	holderRowID := mustInsertHolder(ctx, t, pool, itemID.String(), "linear_release", holderNodeID, "release_to_back", "release_to_head")

	mustResolve(ctx, t, pool, s, itemID.String(), holderNodeID, TerminalCommit)

	// Holder row reflects the release-to-back action.
	state, action := readHolder(ctx, t, pool, holderRowID)
	if state != "completed" {
		t.Fatalf("holder state = %q, want completed", state)
	}
	if action != "release_to_back" {
		t.Fatalf("actual_action = %q, want release_to_back", action)
	}

	// Items-table row went back to 'available'.
	var stateOut string
	if err := pool.QueryRow(ctx, `SELECT state FROM linear_release_items WHERE item_id = $1`, itemID).Scan(&stateOut); err != nil {
		t.Fatalf("inspect items row: %v", err)
	}
	if stateOut != "available" {
		t.Fatalf("items-table state = %q, want available", stateOut)
	}
}

// TestResolveOnTerminal_FanOutBothRelease covers two siblings, both with
// on_commit=release_to_back. The first resolution flips its row to
// 'completed' but sees an active sibling, so does not fire
// ReleaseClaimItem. The second resolution sees zero active siblings and
// zero deletes, so fires the release.
func TestResolveOnTerminal_FanOutBothRelease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	mustCreateItemsTable(t, pool, "fanout_release_items")
	s := mustBuildStore(t, pool, "fanout_release", "fanout_release_items")

	itemID := uuid.New()
	insertItem(ctx, t, pool, "fanout_release_items", itemID, "in_progress")

	tplID, instID, nodeA := mustProvisionTemplateInstanceNode(ctx, t, pool, "fanout_release")
	defer cleanupTemplateInstance(ctx, t, pool, tplID, instID)
	nodeB := mustInsertSiblingNode(ctx, t, pool, instID, "leaf-b")

	mustInsertHolder(ctx, t, pool, itemID.String(), "fanout_release", nodeA, "release_to_back", "release_to_head")
	mustInsertHolder(ctx, t, pool, itemID.String(), "fanout_release", nodeB, "release_to_back", "release_to_head")

	// First resolution (nodeA): items-table still in_progress because nodeB active.
	mustResolve(ctx, t, pool, s, itemID.String(), nodeA, TerminalCommit)
	var stateOut string
	if err := pool.QueryRow(ctx, `SELECT state FROM fanout_release_items WHERE item_id = $1`, itemID).Scan(&stateOut); err != nil {
		t.Fatalf("inspect items row 1: %v", err)
	}
	if stateOut != "in_progress" {
		t.Fatalf("after first resolution: items-table state = %q, want still in_progress", stateOut)
	}

	// Second resolution (nodeB): now items-table goes back to 'available'.
	mustResolve(ctx, t, pool, s, itemID.String(), nodeB, TerminalCommit)
	if err := pool.QueryRow(ctx, `SELECT state FROM fanout_release_items WHERE item_id = $1`, itemID).Scan(&stateOut); err != nil {
		t.Fatalf("inspect items row 2: %v", err)
	}
	if stateOut != "available" {
		t.Fatalf("after second resolution: items-table state = %q, want available", stateOut)
	}
}

// TestResolveOnTerminal_FanOutDeleteWinsFirst: nodeA deletes; nodeB
// later releases. The delete wins; nodeB's row should be 'delete_won'
// (collapsed by nodeA's resolution, which marked all active siblings).
func TestResolveOnTerminal_FanOutDeleteWinsFirst(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	mustCreateItemsTable(t, pool, "delete_first_items")
	s := mustBuildStore(t, pool, "delete_first", "delete_first_items")

	itemID := uuid.New()
	insertItem(ctx, t, pool, "delete_first_items", itemID, "in_progress")

	tplID, instID, nodeA := mustProvisionTemplateInstanceNode(ctx, t, pool, "delete_first")
	defer cleanupTemplateInstance(ctx, t, pool, tplID, instID)
	nodeB := mustInsertSiblingNode(ctx, t, pool, instID, "leaf-b")

	hA := mustInsertHolder(ctx, t, pool, itemID.String(), "delete_first", nodeA, "delete", "release_to_head")
	hB := mustInsertHolder(ctx, t, pool, itemID.String(), "delete_first", nodeB, "release_to_back", "release_to_head")

	// nodeA resolves with commit → delete. Items-table row goes; nodeB's holder collapses to 'delete_won'.
	mustResolve(ctx, t, pool, s, itemID.String(), nodeA, TerminalCommit)

	stateA, actionA := readHolder(ctx, t, pool, hA)
	if stateA != "completed" || actionA != "delete" {
		t.Fatalf("hA: state=%q action=%q, want completed/delete", stateA, actionA)
	}
	stateB, actionB := readHolder(ctx, t, pool, hB)
	if stateB != "completed" || actionB != "delete_won" {
		t.Fatalf("hB after nodeA delete: state=%q action=%q, want completed/delete_won", stateB, actionB)
	}

	// Items-table row gone.
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM delete_first_items WHERE item_id = $1`, itemID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("items-table row count = %d, want 0", n)
	}

	// nodeB resolves later. Should be a no-op (state already completed).
	mustResolve(ctx, t, pool, s, itemID.String(), nodeB, TerminalCommit)
	stateB2, actionB2 := readHolder(ctx, t, pool, hB)
	if stateB2 != "completed" || actionB2 != "delete_won" {
		t.Fatalf("hB after nodeB late resolve: state=%q action=%q, want completed/delete_won (idempotent)", stateB2, actionB2)
	}
}

// TestResolveOnTerminal_FanOutDeleteWinsSecond: nodeA releases first;
// nodeB then deletes. The delete still wins regardless of order — when
// nodeB resolves with delete, it sees no PRIOR_DELETE and triggers the
// items-table delete.
func TestResolveOnTerminal_FanOutDeleteWinsSecond(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	mustCreateItemsTable(t, pool, "delete_second_items")
	s := mustBuildStore(t, pool, "delete_second", "delete_second_items")

	itemID := uuid.New()
	insertItem(ctx, t, pool, "delete_second_items", itemID, "in_progress")

	tplID, instID, nodeA := mustProvisionTemplateInstanceNode(ctx, t, pool, "delete_second")
	defer cleanupTemplateInstance(ctx, t, pool, tplID, instID)
	nodeB := mustInsertSiblingNode(ctx, t, pool, instID, "leaf-b")

	hA := mustInsertHolder(ctx, t, pool, itemID.String(), "delete_second", nodeA, "release_to_back", "release_to_head")
	hB := mustInsertHolder(ctx, t, pool, itemID.String(), "delete_second", nodeB, "delete", "release_to_head")

	// nodeA resolves first with release_to_back. nodeB still active so no items-table mutation.
	mustResolve(ctx, t, pool, s, itemID.String(), nodeA, TerminalCommit)
	stateA, actionA := readHolder(ctx, t, pool, hA)
	if stateA != "completed" || actionA != "release_to_back" {
		t.Fatalf("hA: state=%q action=%q, want completed/release_to_back", stateA, actionA)
	}
	var stateOut string
	if err := pool.QueryRow(ctx, `SELECT state FROM delete_second_items WHERE item_id = $1`, itemID).Scan(&stateOut); err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if stateOut != "in_progress" {
		t.Fatalf("items-table state after nodeA release = %q, want still in_progress (nodeB active)", stateOut)
	}

	// nodeB resolves with delete. Items-table row goes; the delete branch
	// does not collapse hA because hA is already completed (with
	// release_to_back). The delete wins by virtue of the items-table
	// being gone — observers see no claim, regardless of holder bookkeeping.
	mustResolve(ctx, t, pool, s, itemID.String(), nodeB, TerminalCommit)
	stateB, actionB := readHolder(ctx, t, pool, hB)
	if stateB != "completed" || actionB != "delete" {
		t.Fatalf("hB: state=%q action=%q, want completed/delete", stateB, actionB)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM delete_second_items WHERE item_id = $1`, itemID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("items-table row count = %d, want 0 (delete wins regardless of order)", n)
	}
}

// TestResolveOnTerminal_GiveUpUsesOnGiveUp confirms terminal=give_up
// picks the on_give_up action, not on_commit.
func TestResolveOnTerminal_GiveUpUsesOnGiveUp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	mustCreateItemsTable(t, pool, "giveup_items")
	s := mustBuildStore(t, pool, "giveup", "giveup_items")

	itemID := uuid.New()
	insertItem(ctx, t, pool, "giveup_items", itemID, "in_progress")

	tplID, instID, nodeA := mustProvisionTemplateInstanceNode(ctx, t, pool, "giveup")
	defer cleanupTemplateInstance(ctx, t, pool, tplID, instID)
	hA := mustInsertHolder(ctx, t, pool, itemID.String(), "giveup", nodeA, "delete", "release_to_head")

	mustResolve(ctx, t, pool, s, itemID.String(), nodeA, TerminalGiveUp)

	state, action := readHolder(ctx, t, pool, hA)
	if state != "completed" || action != "release_to_head" {
		t.Fatalf("hA: state=%q action=%q, want completed/release_to_head (on_give_up)", state, action)
	}
	// Items row reset to available, enqueued_at far in the past.
	var stateOut string
	var enqAt time.Time
	if err := pool.QueryRow(ctx, `SELECT state, enqueued_at FROM giveup_items WHERE item_id = $1`, itemID).Scan(&stateOut, &enqAt); err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if stateOut != "available" {
		t.Fatalf("items state = %q, want available", stateOut)
	}
	if !enqAt.Before(time.Now().Add(-30 * 24 * time.Hour)) {
		t.Fatalf("enqueued_at = %v, expected to be far in the past (release_to_head)", enqAt)
	}
}

// TestResolveOnTerminal_NoHolderRow: terminal node is not actually a
// holder for this claim. Must no-op silently.
func TestResolveOnTerminal_NoHolderRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	mustCreateItemsTable(t, pool, "noholder_items")
	s := mustBuildStore(t, pool, "noholder", "noholder_items")

	itemID := uuid.New()
	insertItem(ctx, t, pool, "noholder_items", itemID, "in_progress")
	tplID, instID, nodeA := mustProvisionTemplateInstanceNode(ctx, t, pool, "noholder")
	defer cleanupTemplateInstance(ctx, t, pool, tplID, instID)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	txCtx := store.WithTx(ctx, tx)
	if err := s.ResolveOnTerminal(txCtx, itemID.String(), nodeA, TerminalCommit); err != nil {
		t.Fatalf("ResolveOnTerminal: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Items-table row untouched.
	var stateOut string
	if err := pool.QueryRow(ctx, `SELECT state FROM noholder_items WHERE item_id = $1`, itemID).Scan(&stateOut); err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if stateOut != "in_progress" {
		t.Fatalf("items-table state = %q, want in_progress (no-op should not have changed anything)", stateOut)
	}
}

// TestResolveOnTerminal_AlreadyCompletedNoOp confirms idempotent re-call.
func TestResolveOnTerminal_AlreadyCompletedNoOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)
	mustCreateItemsTable(t, pool, "idem_items")
	s := mustBuildStore(t, pool, "idem", "idem_items")

	itemID := uuid.New()
	insertItem(ctx, t, pool, "idem_items", itemID, "in_progress")
	tplID, instID, nodeA := mustProvisionTemplateInstanceNode(ctx, t, pool, "idem")
	defer cleanupTemplateInstance(ctx, t, pool, tplID, instID)

	hA := mustInsertHolder(ctx, t, pool, itemID.String(), "idem", nodeA, "release_to_back", "release_to_back")
	mustResolve(ctx, t, pool, s, itemID.String(), nodeA, TerminalCommit)
	state1, action1 := readHolder(ctx, t, pool, hA)

	// Re-resolve: must be a no-op.
	mustResolve(ctx, t, pool, s, itemID.String(), nodeA, TerminalCommit)
	state2, action2 := readHolder(ctx, t, pool, hA)
	if state1 != state2 || action1 != action2 {
		t.Fatalf("re-resolve mutated row: %q/%q -> %q/%q", state1, action1, state2, action2)
	}
}

// --- helpers ---

func insertItem(ctx context.Context, t *testing.T, pool *pgxpool.Pool, table string, id uuid.UUID, state string) {
	t.Helper()
	q := `INSERT INTO ` + table + ` (item_id, payload, state, claim_token, claimed_at) VALUES ($1, '{}'::jsonb, $2, gen_random_uuid(), now())`
	if _, err := pool.Exec(ctx, q, id, state); err != nil {
		t.Fatalf("insert into %s: %v", table, err)
	}
}

// mustProvisionTemplateInstanceNode creates a (template, instance, leaf-node)
// triple in the rimsky_* tables. Returns (template_id, instance_id, node_id).
// The leaf-node satisfies rimsky_claim_holders.holder_node_id's FK to
// rimsky_nodes.
func mustProvisionTemplateInstanceNode(ctx context.Context, t *testing.T, pool *pgxpool.Pool, label string) (tplID uuid.UUID, instID uuid.UUID, nodeID string) {
	t.Helper()
	tplID = uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO rimsky_templates (id, name, version, spec) VALUES ($1, $2, '1', '{}'::jsonb)`,
		tplID, "tpl-"+label,
	); err != nil {
		t.Fatalf("insert template: %v", err)
	}
	instID = uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO rimsky_instances (id, template_id, consumer_key, params) VALUES ($1, $2, $3, '{}'::jsonb)`,
		instID, tplID, "ck-"+label,
	); err != nil {
		t.Fatalf("insert instance: %v", err)
	}
	leafID := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO rimsky_nodes (id, instance_id, node_type, state, dependencies) VALUES ($1, $2, $3, 'fresh', '{}')`,
		leafID, instID, "leaf-"+label,
	); err != nil {
		t.Fatalf("insert node: %v", err)
	}
	return tplID, instID, leafID.String()
}

func mustInsertSiblingNode(ctx context.Context, t *testing.T, pool *pgxpool.Pool, instID uuid.UUID, label string) string {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO rimsky_nodes (id, instance_id, node_type, state, dependencies) VALUES ($1, $2, $3, 'fresh', '{}')`,
		id, instID, label,
	); err != nil {
		t.Fatalf("insert sibling node: %v", err)
	}
	return id.String()
}

func cleanupTemplateInstance(ctx context.Context, t *testing.T, pool *pgxpool.Pool, tplID, instID uuid.UUID) {
	t.Helper()
	// Best-effort; CASCADE handles nodes/holders.
	if _, err := pool.Exec(ctx, `DELETE FROM rimsky_instances WHERE id = $1`, instID); err != nil {
		t.Logf("cleanup instance: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM rimsky_templates WHERE id = $1`, tplID); err != nil {
		t.Logf("cleanup template: %v", err)
	}
}

func mustInsertHolder(ctx context.Context, t *testing.T, pool *pgxpool.Pool, claimID, storeName, holderNodeID, onCommit, onGiveUp string) string {
	t.Helper()
	rowID := uuid.New().String()
	if _, err := pool.Exec(ctx,
		`INSERT INTO rimsky_claim_holders (id, claim_id, store_name, holder_node_id, on_commit, on_give_up)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		rowID, claimID, storeName, holderNodeID, onCommit, onGiveUp,
	); err != nil {
		t.Fatalf("insert holder: %v", err)
	}
	return rowID
}

func readHolder(ctx context.Context, t *testing.T, pool *pgxpool.Pool, rowID string) (state, actualAction string) {
	t.Helper()
	var action *string
	if err := pool.QueryRow(ctx,
		`SELECT state, actual_action FROM rimsky_claim_holders WHERE id = $1`,
		rowID,
	).Scan(&state, &action); err != nil {
		if err == pgx.ErrNoRows {
			t.Fatalf("holder row %s missing", rowID)
		}
		t.Fatalf("read holder: %v", err)
	}
	if action == nil {
		return state, ""
	}
	return state, *action
}

func mustResolve(ctx context.Context, t *testing.T, pool *pgxpool.Pool, s *Store, claimID, holderNodeID string, outcome TerminalOutcome) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	txCtx := store.WithTx(ctx, tx)
	if err := s.ResolveOnTerminal(txCtx, claimID, holderNodeID, outcome); err != nil {
		t.Fatalf("ResolveOnTerminal(%s,%s,%s): %v", claimID, holderNodeID, outcome, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}
