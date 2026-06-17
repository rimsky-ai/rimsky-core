// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @constraint: DispatchClaimRelease conformance area.
// Inv 4 (claimant-guarded release), inv 6 (orphan cutoff), inv 2
// (dispatch-claim brackets running window).
package conformance

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func testDispatchClaimRelease(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	q := d.Queue()
	if q == nil {
		t.Fatalf("driver.Queue() returned nil")
	}

	if err := q.Enqueue(ctx, persistence.DispatchRequest{
		NodeID:         fix.NodeID,
		ExecutorName:   "test-executor",
		RequiredStores: []string{},
		EnqueuedAt:     time.Now().Add(-1 * time.Second),
		FrameID:        fix.FrameID,
		RunScopeID:     fix.MainRunScopeID,
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// @constraint: two concurrent supervisors racing the same dispatch row
	// must produce exactly one successful ClaimDispatchRow; the other
	// observes a false return.
	supA := "supervisor-A"
	supB := "supervisor-B"

	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		wins   int
		losses int
	)
	tryClaim := func(supID string) {
		defer wg.Done()
		err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
				AcceptedExecutors: []string{"test-executor"},
				AcceptedStores:    []string{},
				Limit:             10,
			})
			if err != nil {
				return err
			}
			for _, c := range cands {
				if c.NodeID != fix.NodeID {
					continue
				}
				ok, err := q.ClaimDispatchRow(ctx, tx, c.DispatchID, supID)
				if err != nil {
					return err
				}
				mu.Lock()
				if ok {
					wins++
				} else {
					losses++
				}
				mu.Unlock()
				return nil
			}
			mu.Lock()
			losses++
			mu.Unlock()
			return nil
		})
		if err != nil {
			t.Errorf("claim tx %s: %v", supID, err)
		}
	}

	wg.Add(2)
	go tryClaim(supA)
	go tryClaim(supB)
	wg.Wait()

	if wins != 1 {
		t.Fatalf("expected exactly 1 winning claim, got wins=%d losses=%d", wins, losses)
	}

	// @deliberate: find the dispatch row via the live-listing surface —
	// the async-orphan sweep no longer surfaces sync-mode dispatch rows,
	// so it cannot serve as a "find by node_id" backstop the way the
	// pre-rewrite test used it. ListLive walks every in-flight dispatch
	// row independent of the async-ack registry.
	live, err := q.ListLive(ctx, persistence.DispatchListFilter{}, persistence.ListPagination{Limit: 50})
	if err != nil {
		t.Fatalf("ListLive: %v", err)
	}
	var dispatchID shared.UUID
	var winner string
	for _, r := range live.Rows {
		if r.NodeID == fix.NodeID {
			dispatchID = r.ID
			if r.ClaimedBy != nil {
				winner = *r.ClaimedBy
			}
			break
		}
	}
	if dispatchID == (shared.UUID{}) {
		t.Fatalf("could not find dispatch row for node %s", fix.NodeID)
	}
	if winner != supA && winner != supB {
		t.Fatalf("unexpected winner %q", winner)
	}
	loser := supA
	if winner == supA {
		loser = supB
	}

	// @constraint: ReleaseClaim called with a non-claimant supervisor must
	// be a no-op — the claimant-guarded release predicate rejects it
	// silently, leaving the original winner as ClaimedBy.
	if err := q.ReleaseClaim(ctx, dispatchID, loser); err != nil {
		t.Fatalf("releaseClaim wrong sup: %v", err)
	}
	owner, err := q.GetClaimedBy(ctx, dispatchID)
	if err != nil {
		t.Fatalf("getClaimedBy: %v", err)
	}
	if owner.Kind != "claimed_by" || owner.SupervisorID != winner {
		t.Fatalf("guard violated: kind=%s sup=%s; want claimed_by/%s",
			owner.Kind, owner.SupervisorID, winner)
	}

	// @constraint: ListOrphanedClaims filters by async_ack_id IS NOT
	// NULL. The per-row per-deadline matrix evaluates in Go in
	// code:SweepExecutorDeadlines (using the denormalized
	// effective_max_quiet_period_seconds and effective_max_runtime_seconds
	// columns). A sync-mode dispatch never carries an async_ack_id, so
	// the orphan-sweep view never surfaces it. The async-mode round-trip
	// is exercised by the ParkResume/RegisterAsyncAckRoundTrip bucket.
	rowsSync, err := q.ListOrphanedClaims(ctx)
	if err != nil {
		t.Fatalf("ListOrphanedClaims: %v", err)
	}
	for _, r := range rowsSync {
		if r.NodeID == fix.NodeID {
			t.Fatalf("sync-mode dispatch row leaked into the async-orphan sweep")
		}
	}
}
