// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: cascade
// @decision: walker-rule-per-sender-node
// @decision: non-cascade-direct-to-stale

package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// @decision: walker-rule-per-sender-node
func testTwoLegClaimPromoteContract(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	q := d.Queue()

	var dispatchID shared.UUID
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := q.EnqueueInTx(ctx, persistence.DispatchRequest{
			NodeID:                 fix.NodeID,
			ExecutorName:           "test-executor",
			RequiredClaimProducers: []string{},
			EnqueuedAt:             time.Now().Add(-1 * time.Second),
			FrameID:                fix.FrameID,
			RunScopeID:             fix.MainRunScopeID,
		}, tx); err != nil {
			return err
		}
		cands, err := q.SelectCandidates(ctx, tx, persistence.SelectCandidatesRequest{
			AcceptedExecutors:      []string{"test-executor"},
			AcceptedClaimProducers: []string{},
			Limit:                  10,
		})
		if err != nil {
			return err
		}
		if len(cands) == 0 {
			t.Fatalf("two-leg contract: candidate not surfaced")
		}
		dispatchID = cands[0].DispatchID
		ok, err := q.ClaimDispatchRow(ctx, tx, dispatchID, "two-leg-sup")
		if err != nil {
			return err
		}
		if !ok {
			t.Fatalf("two-leg contract: ClaimDispatchRow returned !ok")
		}
		return nil
	}); err != nil {
		t.Fatalf("two-leg contract: claim leg: %v", err)
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		latest, err := store.Nodes().GetLatestRunForNode(ctx, tx, fix.NodeID)
		if err != nil {
			return err
		}
		if latest == nil {
			t.Fatalf("two-leg contract: GetLatestRunForNode returned nil after claim")
		}
		if latest.State != cascade.NodeStateStale {
			t.Fatalf("two-leg contract: after ClaimDispatchRow, run state must remain 'stale' until PromoteClaimedToRunning runs; got %q", latest.State)
		}
		if latest.ClaimedBy != "two-leg-sup" {
			t.Fatalf("two-leg contract: after ClaimDispatchRow, claimed_by must equal the claiming supervisor; got %q", latest.ClaimedBy)
		}
		return nil
	}); err != nil {
		t.Fatalf("two-leg contract: post-claim probe: %v", err)
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, err := q.PromoteClaimedToRunning(ctx, tx, dispatchID, "two-leg-sup")
		return err
	}); err != nil {
		t.Fatalf("two-leg contract: promote leg: %v", err)
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		latest, err := store.Nodes().GetLatestRunForNode(ctx, tx, fix.NodeID)
		if err != nil {
			return err
		}
		if latest == nil {
			t.Fatalf("two-leg contract: GetLatestRunForNode returned nil after promote")
		}
		if latest.State != cascade.NodeStateRunning {
			t.Fatalf("two-leg contract: after PromoteClaimedToRunning, run state must be 'running'; got %q", latest.State)
		}
		if latest.ClaimedBy != "two-leg-sup" {
			t.Fatalf("two-leg contract: after PromoteClaimedToRunning, claimed_by must still equal the claiming supervisor; got %q", latest.ClaimedBy)
		}
		return nil
	}); err != nil {
		t.Fatalf("two-leg contract: post-promote probe: %v", err)
	}
}

// @decision: walker-rule-per-sender-node
func testCreateCascadePendingAndFindLatest(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	var pendingID shared.UUID
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		id, err := store.Nodes().CreateCascadePending(ctx, tx, fix.NodeID, fix.MainRunScopeID, fix.FrameID)
		pendingID = id
		return err
	}); err != nil {
		t.Fatalf("CreateCascadePending: %v", err)
	}
	if pendingID == (shared.UUID{}) {
		t.Fatalf("CreateCascadePending returned zero UUID")
	}

	var found *persistence.NodeRunForGate
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := store.Nodes().FindLatestCascadePending(ctx, tx, fix.NodeID, fix.MainRunScopeID, fix.FrameID)
		found = r
		return err
	}); err != nil {
		t.Fatalf("FindLatestCascadePending: %v", err)
	}
	if found == nil {
		t.Fatalf("FindLatestCascadePending returned nil after CreateCascadePending")
	}
	if found.RunID != pendingID {
		t.Fatalf("FindLatestCascadePending returned %s want %s", found.RunID, pendingID)
	}
	if found.State != cascade.NodeStatePending {
		t.Fatalf("FindLatestCascadePending returned state %q want 'pending'", found.State)
	}
	if found.CreationReason != cascade.CreationReasonCascade {
		t.Fatalf("CreateCascadePending should set creation_reason=cascade; got %q", found.CreationReason)
	}
}

// @decision: walker-rule-per-sender-node
func testLockReceiverCascade_NoDeadlock(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.Nodes().LockReceiverCascade(ctx, tx, fix.NodeID, fix.MainRunScopeID, fix.FrameID); err != nil {
			return err
		}
		if err := store.Nodes().LockReceiverCascade(ctx, tx, fix.NodeID, fix.MainRunScopeID, fix.FrameID); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("LockReceiverCascade: postgres advisory-lock or sqlite single-writer model must allow back-to-back same-tx calls; got %v", err)
	}
}

// @decision: non-cascade-direct-to-stale
func testCreateNonCascadeStaleCarriesForward(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	priorRunID := uuid.New()
	priorData := map[string]any{"marker": "from-prior-run"}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if _, err := store.Nodes().CreateNonCascadeStale(ctx, tx, persistence.NonCascadeStaleInput{
			NodeID:         fix.NodeID,
			RunScopeID:     fix.MainRunScopeID,
			FrameID:        fix.FrameID,
			ExecutorName:   "test-executor",
			EnqueuedAt:     time.Now().Add(-time.Minute),
			CreationReason: cascade.CreationReasonRecalculate,
		}); err != nil {
			return err
		}
		latest, err := store.Nodes().GetLatestRunForNode(ctx, tx, fix.NodeID)
		if err != nil {
			return err
		}
		if latest == nil {
			t.Fatalf("CreateNonCascadeStale: no latest run found")
		}
		priorRunID = latest.RunID
		return store.NodeAttributes().Upsert(ctx, latest.RunID, fix.NodeID, priorData, tx)
	}); err != nil {
		t.Fatalf("seed prior run: %v", err)
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.Nodes().UpdateState(ctx, fix.NodeID, fix.MainRunScopeID,
			cascade.NodeStateFresh, cascade.ReasonPureCascade, nil, tx)
	}); err != nil {
		t.Fatalf("settle prior run: %v", err)
	}
	_ = priorRunID

	var newRunID shared.UUID
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		id, err := store.Nodes().CreateNonCascadeStale(ctx, tx, persistence.NonCascadeStaleInput{
			NodeID:         fix.NodeID,
			RunScopeID:     fix.MainRunScopeID,
			FrameID:        fix.FrameID,
			ExecutorName:   "test-executor",
			EnqueuedAt:     time.Now(),
			CreationReason: cascade.CreationReasonOperatorInvalidate,
		})
		newRunID = id
		return err
	}); err != nil {
		t.Fatalf("create non-cascade stale: %v", err)
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		attrs, err := store.NodeAttributes().GetByRun(ctx, newRunID, tx)
		if err != nil {
			return err
		}
		if attrs == nil {
			t.Fatalf("CreateNonCascadeStale must carry forward prior NodeAttributes; got nil row")
		}
		marker, ok := attrs.Data["marker"].(string)
		if !ok || marker != "from-prior-run" {
			t.Fatalf("CreateNonCascadeStale must carry forward prior data; got %+v", attrs.Data)
		}
		snapshot, err := store.NodeAttributes().GetDispatchInputBag(ctx, tx, newRunID)
		if err != nil {
			return err
		}
		if snapshot == nil {
			t.Fatalf("CreateNonCascadeStale must snapshot a dispatch_input_bag at row creation; got nil")
		}
		marker, ok = snapshot["marker"].(string)
		if !ok || marker != "from-prior-run" {
			t.Fatalf("snapshot dispatch_input_bag must match the carried-forward data; got %+v", snapshot)
		}
		return nil
	}); err != nil {
		t.Fatalf("verify carry-forward: %v", err)
	}
}
