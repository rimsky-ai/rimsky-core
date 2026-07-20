// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func testVerifyBeforeRunRead(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	q := d.Queue()

	if err := q.Enqueue(ctx, persistence.DispatchRequest{
		NodeID:                 fix.NodeID,
		ExecutorName:           "test-executor",
		RequiredClaimProducers: []string{},
		EnqueuedAt:             time.Now().Add(-1 * time.Second),
		FrameID:                fix.FrameID,
		RunScopeID:             fix.MainRunScopeID,
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	supID := "verify-supervisor"
	var nodeRunID shared.UUID
	if err := d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors:      []string{"test-executor"},
			AcceptedClaimProducers: []string{},
			Limit:                  10,
		})
		if err != nil {
			return err
		}
		if len(cands) != 1 {
			t.Fatalf("expected 1 candidate, got %d", len(cands))
		}
		nodeRunID = cands[0].NodeRunID
		ok, err := q.ClaimDispatchRow(ctx, tx, cands[0].NodeRunID, supID)
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

	owner, err := q.GetClaimedBy(ctx, nodeRunID)
	if err != nil {
		t.Fatalf("GetClaimedBy: %v", err)
	}
	if owner.Kind != "claimed_by" || owner.SupervisorID != supID {
		t.Fatalf("expected claimed_by/%s, got kind=%s sup=%s", supID, owner.Kind, owner.SupervisorID)
	}

	if err := q.ReleaseClaim(ctx, nodeRunID, supID); err != nil {
		t.Fatalf("ReleaseClaim: %v", err)
	}
	owner2, err := q.GetClaimedBy(ctx, nodeRunID)
	if err != nil {
		t.Fatalf("GetClaimedBy: %v", err)
	}
	if owner2.Kind != "unclaimed" {
		t.Fatalf("expected unclaimed, got %s/%s", owner2.Kind, owner2.SupervisorID)
	}

	missing, err := q.GetClaimedBy(ctx, shared.UUID(uuid.New()))
	if err != nil {
		t.Fatalf("GetClaimedBy (missing run): %v", err)
	}
	if missing.Kind != "not_found" {
		t.Fatalf("GetClaimedBy for a nonexistent node_run_id: expected kind=not_found, got %s/%s", missing.Kind, missing.SupervisorID)
	}
}
