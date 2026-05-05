// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// verify.go — VerifyBeforeRunRead conformance area.
//
// Inv 5: verify-before-run.
package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/shared"
)

func testVerifyBeforeRunRead(t *testing.T, d persistence.Driver) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	q := d.Queue()

	if err := q.Enqueue(ctx, persistence.DispatchRequest{
		NodeID:         fix.NodeID,
		ExecutorName:   "test-executor",
		RequiredStores: []string{},
		EnqueuedAt:     time.Now().Add(-1 * time.Second),
		FrameID:        fix.FrameID,
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	supID := "verify-supervisor"
	var dispatchID shared.UUID
	if err := d.Store().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors: []string{"test-executor"},
			AcceptedStores:    []string{},
			Limit:             10,
		})
		if err != nil {
			return err
		}
		if len(cands) != 1 {
			t.Fatalf("expected 1 candidate, got %d", len(cands))
		}
		dispatchID = cands[0].DispatchID
		ok, err := q.ClaimDispatchRow(ctx, tx, cands[0].DispatchID, supID)
		if err != nil {
			return err
		}
		if !ok {
			t.Fatalf("claim was not successful")
		}
		return nil
	}); err != nil {
		t.Fatalf("claim tx: %v", err)
	}

	// Re-read via GetClaimedBy returns current owner.
	owner, err := q.GetClaimedBy(ctx, dispatchID)
	if err != nil {
		t.Fatalf("GetClaimedBy: %v", err)
	}
	if owner.Kind != "claimed_by" || owner.SupervisorID != supID {
		t.Fatalf("expected claimed_by/%s, got kind=%s sup=%s", supID, owner.Kind, owner.SupervisorID)
	}

	// Manually clear the claim, then re-read returns "unclaimed".
	if err := q.ReleaseClaim(ctx, dispatchID, supID); err != nil {
		t.Fatalf("ReleaseClaim: %v", err)
	}
	owner2, err := q.GetClaimedBy(ctx, dispatchID)
	if err != nil {
		t.Fatalf("GetClaimedBy: %v", err)
	}
	if owner2.Kind != "unclaimed" {
		t.Fatalf("expected unclaimed, got %s/%s", owner2.Kind, owner2.SupervisorID)
	}
}
