// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// dispatch.go — DispatchClaimRelease conformance area.
//
// Inv 4 (claimant-guarded release), inv 6 (orphan cutoff), inv 2
// (dispatch-claim brackets running window).
package conformance

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/foundation/shared"
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

	// Two concurrent supervisors race the same dispatch row. Exactly one
	// should win the claim.
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

	// Find the dispatch row id + winner via Queue.GetDispatchNode-ish path.
	// We don't have a "find by node_id" helper; instead, pull from
	// ListOrphanedClaims with a future cutoff (returns rows under any
	// last_heartbeat_at).
	rows, err := q.ListOrphanedClaims(ctx, time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatalf("ListOrphanedClaims: %v", err)
	}
	var dispatchID shared.UUID
	var winner string
	for _, r := range rows {
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

	// ReleaseClaim with the wrong supervisor is a no-op (claimant guard).
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

	// ListOrphanedClaims cutoff: the row's last_heartbeat_at was set to
	// "now" at claim time. A cutoff in the past returns nothing; a cutoff
	// in the future returns the row.
	pastCutoff := time.Now().Add(-1 * time.Hour)
	rowsPast, err := q.ListOrphanedClaims(ctx, pastCutoff)
	if err != nil {
		t.Fatalf("ListOrphanedClaims past: %v", err)
	}
	for _, r := range rowsPast {
		if r.NodeID == fix.NodeID {
			t.Fatalf("expected fixture row absent from past-cutoff orphan list")
		}
	}
	rowsFuture, err := q.ListOrphanedClaims(ctx, time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatalf("ListOrphanedClaims future: %v", err)
	}
	found := false
	for _, r := range rowsFuture {
		if r.NodeID == fix.NodeID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected fixture row in future-cutoff orphan list")
	}
}
