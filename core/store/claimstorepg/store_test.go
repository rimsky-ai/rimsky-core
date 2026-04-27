package claimstorepg

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fallguy/rimsky/core/internal/pgtest"
	"github.com/fallguy/rimsky/core/store"
)

// TestStore_AcquireLock_FIFO inserts three items, claims them in order,
// and confirms the items-table state transitions match §13.3 / §9.10.
func TestStore_AcquireLock_FIFO(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	mustCreateItemsTable(t, pool, "fifo_items")
	s := mustBuildStore(t, pool, "fifo", "fifo_items")

	// Insert three items with strictly increasing enqueued_at so FIFO
	// order is observable.
	type item struct {
		id  uuid.UUID
		seq int
	}
	items := make([]item, 3)
	base := time.Now().UTC()
	for i := range items {
		items[i] = item{id: uuid.New(), seq: i}
		// Stagger enqueued_at by milliseconds so FIFO order is observable
		// without relying on insert ordering.
		enqueuedAt := base.Add(time.Duration(i) * time.Millisecond)
		if _, err := pool.Exec(ctx,
			`INSERT INTO fifo_items (item_id, payload, enqueued_at)
			 VALUES ($1, $2, $3)`,
			items[i].id, marshalJSON(t, map[string]any{"seq": i}), enqueuedAt,
		); err != nil {
			t.Fatalf("insert item %d: %v", i, err)
		}
	}

	// Three sequential acquisitions, each in its own tx (mirroring the
	// supervisor's atomic acquisition shape).
	for i := 0; i < 3; i++ {
		got := acquireOnce(ctx, t, pool, s)
		if got.ClaimID == "" {
			t.Fatalf("acquisition %d: empty ClaimID — pool exhausted prematurely", i)
		}
		if got.ClaimID != items[i].id.String() {
			t.Fatalf("acquisition %d: claim id = %q, want %q (FIFO order broken)", i, got.ClaimID, items[i].id.String())
		}
		payload, ok := got.Payload.(map[string]any)
		if !ok {
			t.Fatalf("acquisition %d: payload type %T, want map[string]any", i, got.Payload)
		}
		if int(payload["seq"].(float64)) != i {
			t.Fatalf("acquisition %d: payload[\"seq\"] = %v, want %d", i, payload["seq"], i)
		}
		assertItemState(ctx, t, pool, "fifo_items", items[i].id.String(), "in_progress")
	}

	// Fourth acquisition: pool empty, expect zero-valued ClaimResult.
	got := acquireOnce(ctx, t, pool, s)
	if got.ClaimID != "" {
		t.Fatalf("expected empty ClaimResult on empty pool, got ClaimID=%q", got.ClaimID)
	}
}

// TestStore_AcquireLock_RequiresTx confirms AcquireLock errors out when
// no tx is attached to the context.
func TestStore_AcquireLock_RequiresTx(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	mustCreateItemsTable(t, pool, "no_tx_items")
	s := mustBuildStore(t, pool, "no_tx", "no_tx_items")

	_, _, err := s.AcquireLock(ctx, store.ClaimLockSpec{StoreName: "no_tx"})
	if err == nil {
		t.Fatalf("expected error when AcquireLock called without tx in ctx")
	}
}

// TestStore_AcquireLock_RequiresClaimSpec confirms a non-claim spec is
// rejected.
func TestStore_AcquireLock_RequiresClaimSpec(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	mustCreateItemsTable(t, pool, "non_claim_items")
	s := mustBuildStore(t, pool, "non_claim", "non_claim_items")

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	txCtx := store.WithTx(ctx, tx)
	_, _, err = s.AcquireLock(txCtx, store.NamedLockSpec{Name: "x"})
	if err == nil {
		t.Fatalf("expected error for non-claim spec")
	}
}

// TestStore_HasClaimableItem reports presence of available items.
func TestStore_HasClaimableItem(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	mustCreateItemsTable(t, pool, "claimable_items")
	s := mustBuildStore(t, pool, "claimable", "claimable_items")

	// Empty pool: false.
	got, err := s.HasClaimableItem(ctx, nil)
	if err != nil {
		t.Fatalf("HasClaimableItem (empty): %v", err)
	}
	if got {
		t.Fatalf("HasClaimableItem on empty pool = true, want false")
	}

	// Insert one available row: true.
	if _, err := pool.Exec(ctx,
		`INSERT INTO claimable_items (item_id, payload) VALUES ($1, '{}'::jsonb)`,
		uuid.New(),
	); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err = s.HasClaimableItem(ctx, nil)
	if err != nil {
		t.Fatalf("HasClaimableItem (with row): %v", err)
	}
	if !got {
		t.Fatalf("HasClaimableItem on non-empty pool = false, want true")
	}

	// Flip the only row to in_progress: false again.
	if _, err := pool.Exec(ctx,
		`UPDATE claimable_items SET state = 'in_progress', claim_token = $1, claimed_at = now()`,
		uuid.New(),
	); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err = s.HasClaimableItem(ctx, nil)
	if err != nil {
		t.Fatalf("HasClaimableItem (in_progress): %v", err)
	}
	if got {
		t.Fatalf("HasClaimableItem on in-progress-only pool = true, want false")
	}
}

// TestStore_LockEligible_DefersToHasClaimable verifies LockEligible
// returns the same value as HasClaimableItem for ClaimLockSpec, and false
// for non-claim specs.
func TestStore_LockEligible_DefersToHasClaimable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	mustCreateItemsTable(t, pool, "eligible_items")
	s := mustBuildStore(t, pool, "eligible", "eligible_items")

	// Empty pool, ClaimLockSpec: false.
	got, err := s.LockEligible(ctx, store.ClaimLockSpec{StoreName: "eligible"})
	if err != nil {
		t.Fatalf("LockEligible: %v", err)
	}
	if got {
		t.Fatalf("LockEligible (empty) = true, want false")
	}

	// Insert a row, ClaimLockSpec: true.
	if _, err := pool.Exec(ctx,
		`INSERT INTO eligible_items (item_id, payload) VALUES ($1, '{}'::jsonb)`,
		uuid.New(),
	); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err = s.LockEligible(ctx, store.ClaimLockSpec{StoreName: "eligible"})
	if err != nil {
		t.Fatalf("LockEligible: %v", err)
	}
	if !got {
		t.Fatalf("LockEligible (non-empty) = false, want true")
	}

	// Non-claim spec: always false (fail-closed).
	got, err = s.LockEligible(ctx, store.NamedLockSpec{Name: "x"})
	if err != nil {
		t.Fatalf("LockEligible (named): %v", err)
	}
	if got {
		t.Fatalf("LockEligible (named) = true, want false")
	}
}

// TestStore_OpenHandle echoes claim payload + ID into a ClaimStoreHandle.
func TestStore_OpenHandle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	mustCreateItemsTable(t, pool, "open_handle_items")
	s := mustBuildStore(t, pool, "oh", "open_handle_items")

	payload := map[string]any{"foo": "bar"}
	claimID := uuid.New().String()
	hctx := WithHandleData(ctx, payload, claimID)

	nh, err := s.OpenHandle(hctx, store.LockHandle{}, false)
	if err != nil {
		t.Fatalf("OpenHandle: %v", err)
	}
	ch, ok := nh.(store.ClaimStoreHandle)
	if !ok {
		t.Fatalf("OpenHandle returned %T, want ClaimStoreHandle", nh)
	}
	if ch.ClaimID != claimID {
		t.Fatalf("ClaimID = %q, want %q", ch.ClaimID, claimID)
	}
	if ch.StoreName != "oh" {
		t.Fatalf("StoreName = %q, want %q", ch.StoreName, "oh")
	}
	got, ok := ch.Payload.(map[string]any)
	if !ok {
		t.Fatalf("Payload type %T", ch.Payload)
	}
	if got["foo"] != "bar" {
		t.Fatalf("Payload = %+v", got)
	}
}

// TestStore_ReleaseClaimItem_Back / Head exercise the items-table
// repositioning. We verify enqueued_at moves into the future
// (release_to_back) or far into the past (release_to_head).
func TestStore_ReleaseClaimItem_Back(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	mustCreateItemsTable(t, pool, "release_back_items")
	s := mustBuildStore(t, pool, "rb", "release_back_items")

	itemID := uuid.New()
	originalEnqueue := time.Now().Add(-1 * time.Hour) // explicit "old" enqueued_at
	if _, err := pool.Exec(ctx,
		`INSERT INTO release_back_items (item_id, payload, enqueued_at, state, claim_token, claimed_at)
		 VALUES ($1, '{}'::jsonb, $2, 'in_progress', gen_random_uuid(), now())`,
		itemID, originalEnqueue,
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	txCtx := store.WithTx(ctx, tx)
	if err := s.ReleaseClaimItem(txCtx, itemID.String(), "release_to_back"); err != nil {
		t.Fatalf("ReleaseClaimItem(release_to_back): %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var (
		state   string
		token   *uuid.UUID
		enqAt   time.Time
		claimAt *time.Time
	)
	if err := pool.QueryRow(ctx,
		`SELECT state, claim_token, enqueued_at, claimed_at FROM release_back_items WHERE item_id = $1`,
		itemID,
	).Scan(&state, &token, &enqAt, &claimAt); err != nil {
		t.Fatalf("inspect row: %v", err)
	}
	if state != "available" {
		t.Fatalf("state = %q, want available", state)
	}
	if token != nil {
		t.Fatalf("claim_token = %v, want nil", token)
	}
	if claimAt != nil {
		t.Fatalf("claimed_at = %v, want nil", claimAt)
	}
	// release_to_back stamps enqueued_at to "now"; verify it advanced past
	// the original (1 hour ago) timestamp.
	if !enqAt.After(originalEnqueue.Add(30 * time.Minute)) {
		t.Fatalf("enqueued_at = %v, expected to have advanced past %v", enqAt, originalEnqueue)
	}
}

func TestStore_ReleaseClaimItem_Head(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	mustCreateItemsTable(t, pool, "release_head_items")
	s := mustBuildStore(t, pool, "rh", "release_head_items")

	itemID := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO release_head_items (item_id, payload, state, claim_token, claimed_at)
		 VALUES ($1, '{}'::jsonb, 'in_progress', gen_random_uuid(), now())`,
		itemID,
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	txCtx := store.WithTx(ctx, tx)
	if err := s.ReleaseClaimItem(txCtx, itemID.String(), "release_to_head"); err != nil {
		t.Fatalf("ReleaseClaimItem(release_to_head): %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var enqAt time.Time
	if err := pool.QueryRow(ctx,
		`SELECT enqueued_at FROM release_head_items WHERE item_id = $1`,
		itemID,
	).Scan(&enqAt); err != nil {
		t.Fatalf("inspect row: %v", err)
	}
	// release_to_head pushes enqueued_at one year into the past.
	if !enqAt.Before(time.Now().Add(-30 * 24 * time.Hour)) {
		t.Fatalf("enqueued_at = %v, expected to be far in the past", enqAt)
	}
}

// TestStore_ReleaseClaimItem_DeleteIsRejected confirms ReleaseClaimItem
// rejects the delete actions; those go through the §5.6.4 algorithm.
func TestStore_ReleaseClaimItem_DeleteIsRejected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	mustCreateItemsTable(t, pool, "rdr_items")
	s := mustBuildStore(t, pool, "rdr", "rdr_items")

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	txCtx := store.WithTx(ctx, tx)
	for _, action := range []string{"delete", "delete_won", "bogus"} {
		if err := s.ReleaseClaimItem(txCtx, uuid.New().String(), action); err == nil {
			t.Fatalf("expected error for action %q", action)
		}
	}
}

// TestStore_ReleaseLock_AlwaysNoOp verifies that ReleaseLock is a no-op
// for all action vocabularies (the items-table mutation path is owned by
// ReleaseClaimItem and ResolveOnTerminal, not ReleaseLock).
func TestStore_ReleaseLock_AlwaysNoOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)

	mustCreateItemsTable(t, pool, "noop_release_items")
	s := mustBuildStore(t, pool, "noop", "noop_release_items")

	for _, a := range []store.ReleaseAction{
		store.ReleaseCommit,
		store.ReleaseDiscard,
		store.ReleaseGiveUp,
		store.ReleasePreserveResume,
	} {
		if err := s.ReleaseLock(ctx, store.LockHandle{}, a); err != nil {
			t.Fatalf("ReleaseLock(%q): %v", a, err)
		}
	}
}

// TestStore_RegionsAndUnmarshal sanity-checks the always-false /
// always-nil region surface.
func TestStore_RegionsAndUnmarshal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)
	mustCreateItemsTable(t, pool, "regions_items")
	s := mustBuildStore(t, pool, "regions", "regions_items")

	if s.RegionsConflict(nil, nil) {
		t.Fatalf("RegionsConflict = true, want false")
	}
	got, err := s.UnmarshalRegion([]byte(`{"x":1}`))
	if err != nil {
		t.Fatalf("UnmarshalRegion: %v", err)
	}
	if got != nil {
		t.Fatalf("UnmarshalRegion = %v, want nil", got)
	}
	got2, err := s.HasPriorWork(ctx, store.ClaimLockSpec{})
	if err != nil {
		t.Fatalf("HasPriorWork: %v", err)
	}
	if got2 {
		t.Fatalf("HasPriorWork = true, want false")
	}
}

// TestStore_Commit returns Changed=true unconditionally.
func TestStore_Commit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := pgtest.StartPostgres(ctx, t)
	t.Cleanup(teardown)
	mustCreateItemsTable(t, pool, "commit_items")
	s := mustBuildStore(t, pool, "cmt", "commit_items")

	cr, err := s.Commit(ctx, store.LockHandle{})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !cr.Changed {
		t.Fatalf("Commit.Changed = false, want true")
	}
}

// --- helpers ---

func mustBuildStore(t *testing.T, pool *pgxpool.Pool, name, table string) *Store {
	t.Helper()
	s, err := Factory{Pool: pool}.Build(name, map[string]any{
		"backend":                    "postgres",
		"items_table":                table,
		"on_commit_default":          "delete",
		"on_give_up_default":         "release_to_head",
		"visibility_timeout_seconds": 300,
	})
	if err != nil {
		t.Fatalf("Build %s: %v", name, err)
	}
	return s.(*Store)
}

// acquireOnce runs a single AcquireLock inside its own tx, commits, and
// returns the ClaimResult. Mirrors the supervisor's atomic-acquisition
// outer-tx shape.
func acquireOnce(ctx context.Context, t *testing.T, pool *pgxpool.Pool, s *Store) store.ClaimResult {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	txCtx := store.WithTx(ctx, tx)
	_, cr, err := s.AcquireLock(txCtx, store.ClaimLockSpec{StoreName: s.Name()})
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return cr
}

func assertItemState(ctx context.Context, t *testing.T, pool *pgxpool.Pool, table, itemID, want string) {
	t.Helper()
	var got string
	q := `SELECT state FROM ` + table + ` WHERE item_id = $1`
	if err := pool.QueryRow(ctx, q, itemID).Scan(&got); err != nil {
		if err == pgx.ErrNoRows {
			t.Fatalf("item %s not found in %s", itemID, table)
		}
		t.Fatalf("query state: %v", err)
	}
	if got != want {
		t.Fatalf("item %s state = %q, want %q", itemID, got, want)
	}
}

func marshalJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
