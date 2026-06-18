// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
