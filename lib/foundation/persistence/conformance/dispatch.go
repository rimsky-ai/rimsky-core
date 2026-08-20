// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package conformance

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
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
		NodeID:                 fix.NodeID,
		ExecutorName:           "test-executor",
		RequiredClaimProducers: []string{},
		EnqueuedAt:             time.Now().Add(-1 * time.Second),
		FrameID:                fix.FrameID,
		RunScopeID:             fix.MainRunScopeID,
	}, nil); err != nil {
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
			cands, err := q.SelectCandidates(ctx, persistence.SelectCandidatesRequest{
				Limit: 10,
			}, tx)
			if err != nil {
				return err
			}
			for _, c := range cands {
				if c.NodeID != fix.NodeID {
					continue
				}
				ok, err := q.ClaimDispatchRow(ctx, c.NodeRunID, supID, tx)
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
	var nodeRunID shared.UUID
	var winner string
	for _, r := range live.Rows {
		if r.NodeID == fix.NodeID {
			nodeRunID = r.ID
			if r.ClaimedBy != nil {
				winner = *r.ClaimedBy
			}
			break
		}
	}
	if nodeRunID == (shared.UUID{}) {
		t.Fatalf("could not find dispatch row for node %s", fix.NodeID)
	}
	if winner != supA && winner != supB {
		t.Fatalf("unexpected winner %q", winner)
	}
	loser := supA
	if winner == supA {
		loser = supB
	}

	if err := q.ReleaseClaim(ctx, nodeRunID, loser); err != nil {
		t.Fatalf("releaseClaim wrong sup: %v", err)
	}
	owner, err := q.GetClaimedBy(ctx, nodeRunID)
	if err != nil {
		t.Fatalf("getClaimedBy: %v", err)
	}
	if owner.Kind != persistence.ClaimOwnershipKindClaimedBy || owner.SupervisorID != winner {
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

// @concept: node-run
// @concept: transition-reason
func testDispatchReleaseClaimRefusesTerminalRun(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	q := d.Queue()
	supID := "supervisor-terminal-race"
	nodeRunID := seedClaimedGuardRun(ctx, t, d, fix, supID)

	sig := "terminal/error/test_failure"
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return d.Tables().Nodes().UpdateState(ctx, nodeRunID,
			cascade.NodeStateFailed, cascade.ReasonPolicyGiveUp, &sig, tx)
	}); err != nil {
		t.Fatalf("settle run terminal ahead of a delayed release race: %v", err)
	}

	if err := q.ReleaseClaim(ctx, nodeRunID, supID); !errors.Is(err, cascade.ErrIllegalTransition) {
		t.Fatalf("ReleaseClaim on a terminal run must fail with the illegal-transition sentinel; got %v", err)
	}
	if err := q.ReleaseClaimWithDisposition(ctx, nodeRunID, supID, "stale_recovery"); !errors.Is(err, cascade.ErrIllegalTransition) {
		t.Fatalf("ReleaseClaimWithDisposition on a terminal run must fail with the illegal-transition sentinel; got %v", err)
	}

	var gate *persistence.NodeRunForGate
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		g, err := d.Tables().Nodes().GetRunForGate(ctx, nodeRunID, tx)
		gate = g
		return err
	}); err != nil {
		t.Fatalf("GetRunForGate: %v", err)
	}
	if gate == nil {
		t.Fatalf("run %s vanished", nodeRunID)
	}
	if gate.State != cascade.NodeStateFailed {
		t.Fatalf("a delayed duplicate-acquisition release must not revert an already-terminal run: "+
			"got state=%s want=%s", gate.State, cascade.NodeStateFailed)
	}
}
