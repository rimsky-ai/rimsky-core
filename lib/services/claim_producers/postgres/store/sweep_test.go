// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package store

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
	claimproducer "github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

func bootSweepStore(t *testing.T, table string, visibilityTimeout time.Duration) (*pgxpool.Pool, *Store) {
	t.Helper()
	pool, dsn := bootPostgresTestContainer(t)
	createTestItemsTable(t, pool, table)

	pp := &PickPolicy{
		ItemsTable:        table,
		OnCommit:          action.Action{Kind: action.Recycle},
		OnGiveUp:          action.Action{Kind: action.Recycle},
		VisibilityTimeout: visibilityTimeout,
	}
	st, err := New(context.Background(), Config{
		Connection:     dsn,
		WriteSemantics: claimproducer.WriteSemanticsStagedAsync,
		PickPolicies:   map[string]*PickPolicy{"@queue": pp},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(st.Close)
	return pool, st
}

func TestSweepOnce_RestampsExpiredItemToBackOfQueueLikeRecycle(t *testing.T) {
	pool, st := bootSweepStore(t, "sweep_restamp", time.Second)
	ctx := context.Background()

	if _, err := pool.Exec(ctx,
		`INSERT INTO sweep_restamp (item_id, payload, state) VALUES ('stuck', '{}'::jsonb, 'available')`); err != nil {
		t.Fatalf("insert stuck item: %v", err)
	}
	stuckOut, err := st.Open(ctx, "claim-stuck", "@queue", claimproducer.IntentReadWrite)
	if err != nil {
		t.Fatalf("Open stuck: %v", err)
	}
	if !stuckOut.Available {
		t.Fatalf("Open stuck: expected Available")
	}
	if _, err := pool.Exec(ctx,
		`UPDATE sweep_restamp SET claimed_at = now() - interval '10 seconds' WHERE claim_token = 'claim-stuck'`); err != nil {
		t.Fatalf("backdate claimed_at: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO sweep_restamp (item_id, payload, state) VALUES ('fresh', '{}'::jsonb, 'available')`); err != nil {
		t.Fatalf("insert fresh item: %v", err)
	}

	if err := st.sweepOnce(ctx); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	var stuckSeq, freshSeq int64
	if err := pool.QueryRow(ctx, `SELECT sequence FROM sweep_restamp WHERE item_id = 'stuck'`).Scan(&stuckSeq); err != nil {
		t.Fatalf("query stuck sequence: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT sequence FROM sweep_restamp WHERE item_id = 'fresh'`).Scan(&freshSeq); err != nil {
		t.Fatalf("query fresh sequence: %v", err)
	}
	if stuckSeq <= freshSeq {
		t.Fatalf("swept item sequence (%d) must be bumped past the fresh item's sequence (%d), matching Recycle's "+
			"back-of-queue behavior, so a repeatedly-timing-out item cannot starve other available work",
			stuckSeq, freshSeq)
	}

	var state string
	if err := pool.QueryRow(ctx, `SELECT state FROM sweep_restamp WHERE item_id = 'stuck'`).Scan(&state); err != nil {
		t.Fatalf("query stuck state: %v", err)
	}
	if state != "available" {
		t.Fatalf("swept item state = %q, want available", state)
	}
}

func TestSweepOnce_SkipsPolicyWithoutVisibilityTimeout(t *testing.T) {
	pool, _ := bootPostgresTestContainer(t)
	createTestItemsTable(t, pool, "sweep_no_timeout")

	st := NewForTest()
	st.SetPoolForTest(pool)
	st.SetPickPoliciesForTest(map[string]*PickPolicy{
		"@queue": {ItemsTable: "sweep_no_timeout", VisibilityTimeout: 0},
	})
	ctx := context.Background()

	if _, err := pool.Exec(ctx,
		`INSERT INTO sweep_no_timeout (item_id, payload, state, claim_token, claimed_at)
		 VALUES ('stuck', '{}'::jsonb, 'in_progress', 'claim-x', now() - interval '1 hour')`); err != nil {
		t.Fatalf("insert stuck item: %v", err)
	}

	if err := st.sweepOnce(ctx); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	var state string
	if err := pool.QueryRow(ctx, `SELECT state FROM sweep_no_timeout WHERE item_id = 'stuck'`).Scan(&state); err != nil {
		t.Fatalf("query state: %v", err)
	}
	if state != "in_progress" {
		t.Fatalf("state = %q, want in_progress unchanged (VisibilityTimeout=0 disables the sweep)", state)
	}
}

func TestSweepOnce_PropagatesPerPolicyErrors(t *testing.T) {
	pool, _ := bootPostgresTestContainer(t)

	st := NewForTest()
	st.SetPoolForTest(pool)
	st.SetPickPoliciesForTest(map[string]*PickPolicy{
		"@queue": {ItemsTable: "nonexistent_items_table", VisibilityTimeout: time.Second},
	})

	if err := st.sweepOnce(context.Background()); err == nil {
		t.Fatal("sweepOnce: expected a non-nil error when the underlying UPDATE fails, got nil " +
			"(RunSweep's warning branch must be reachable, not dead code)")
	}
}
