// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: cascade
// @decision: walker-rule-per-sender-node
// @decision: non-cascade-direct-to-stale

package conformance

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

// @decision: walker-rule-per-sender-node
func testTwoLegClaimPromoteContract(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	q := d.Queue()

	var nodeRunID shared.UUID
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := q.Enqueue(ctx, persistence.DispatchRequest{
			NodeID:                 fix.NodeID,
			ExecutorName:           "test-executor",
			RequiredClaimProducers: []string{},
			EnqueuedAt:             time.Now().Add(-1 * time.Second),
			FrameID:                fix.FrameID,
			RunScopeID:             fix.MainRunScopeID,
		}, tx); err != nil {
			return err
		}
		cands, err := q.SelectCandidates(ctx, persistence.SelectCandidatesRequest{
			Limit: 10,
		}, tx)
		if err != nil {
			return err
		}
		if len(cands) == 0 {
			t.Fatalf("two-leg contract: candidate not surfaced")
		}
		nodeRunID = cands[0].NodeRunID
		ok, err := q.ClaimDispatchRow(ctx, nodeRunID, "two-leg-sup", tx)
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
		latest, err := store.Nodes().GetLatestRunForNode(ctx, fix.NodeID, tx)
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
		_, err := q.PromoteClaimedToRunning(ctx, nodeRunID, "two-leg-sup", tx)
		return err
	}); err != nil {
		t.Fatalf("two-leg contract: promote leg: %v", err)
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		latest, err := store.Nodes().GetLatestRunForNode(ctx, fix.NodeID, tx)
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
		id, err := store.Nodes().CreateCascadePending(ctx, fix.NodeID, fix.MainRunScopeID, fix.FrameID, tx)
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
		r, err := store.Nodes().FindLatestCascadePending(ctx, fix.NodeID, fix.MainRunScopeID, fix.FrameID, tx)
		found = r
		return err
	}); err != nil {
		t.Fatalf("FindLatestCascadePending: %v", err)
	}
	if found == nil {
		t.Fatalf("FindLatestCascadePending returned nil after CreateCascadePending")
	}
	if found.NodeRunID != pendingID {
		t.Fatalf("FindLatestCascadePending returned %s want %s", found.NodeRunID, pendingID)
	}
	if found.State != cascade.NodeStatePending {
		t.Fatalf("FindLatestCascadePending returned state %q want 'pending'", found.State)
	}
	if found.CreationReason != cascade.CreationReasonCascade {
		t.Fatalf("CreateCascadePending should set creation_reason=cascade; got %q", found.CreationReason)
	}

	var secondPendingID shared.UUID
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		id, err := store.Nodes().CreateCascadePending(ctx, fix.NodeID, fix.MainRunScopeID, fix.FrameID, tx)
		secondPendingID = id
		return err
	}); err != nil {
		t.Fatalf("CreateCascadePending (second): %v", err)
	}
	if secondPendingID == pendingID {
		t.Fatalf("second CreateCascadePending returned the same id as the first: %s", secondPendingID)
	}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := store.Nodes().FindLatestCascadePending(ctx, fix.NodeID, fix.MainRunScopeID, fix.FrameID, tx)
		found = r
		return err
	}); err != nil {
		t.Fatalf("FindLatestCascadePending (after second create): %v", err)
	}
	if found == nil || found.NodeRunID != secondPendingID {
		t.Fatalf("FindLatestCascadePending must return the most-recently-created pending row "+
			"(concept:node-run allows multiple coexisting pending rows per (node, run-scope) under cascade "+
			"accumulation); got %v, want the second-created row %s", found, secondPendingID)
	}

	scopeB := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID:               scopeB,
			ParentRunScopeID: &fix.MainRunScopeID,
			ParentNodeRunID:  &secondPendingID,
			GraphName:        spec.MainGraphName,
			InstanceID:       fix.InstanceID,
			PartitionKey:     "cascade-walker-scope-b",
		}, tx)
	}); err != nil {
		t.Fatalf("Create scope B: %v", err)
	}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := store.Nodes().FindLatestCascadePending(ctx, fix.NodeID, scopeB, fix.FrameID, tx)
		found = r
		return err
	}); err != nil {
		t.Fatalf("FindLatestCascadePending (scope B): %v", err)
	}
	if found != nil {
		t.Fatalf("FindLatestCascadePending must scope by run_scope_id: querying an unrelated scope "+
			"(scope B) must not return the pending row created under the main run scope; got %+v", found)
	}
}

// @decision: walker-rule-per-sender-node
func testLockReceiverCascade_NoDeadlock(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := store.Nodes().LockReceiverCascade(ctx, fix.NodeID, fix.MainRunScopeID, fix.FrameID, tx); err != nil {
			return err
		}
		if err := store.Nodes().LockReceiverCascade(ctx, fix.NodeID, fix.MainRunScopeID, fix.FrameID, tx); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("LockReceiverCascade: postgres advisory-lock or sqlite single-writer model must allow back-to-back same-tx calls; got %v", err)
	}

	const workers = 32
	const widenReadsBetweenCheckAndCreate = 50
	var wg sync.WaitGroup
	start := make(chan struct{})
	created := make([]int32, workers)
	errCh := make(chan error, workers)
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		i := i
		go func() {
			defer wg.Done()
			<-start
			err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
				if err := store.Nodes().LockReceiverCascade(ctx, fix.NodeID, fix.MainRunScopeID, fix.FrameID, tx); err != nil {
					return err
				}
				existing, err := store.Nodes().FindLatestCascadePending(ctx, fix.NodeID, fix.MainRunScopeID, fix.FrameID, tx)
				if err != nil {
					return err
				}
				if existing != nil {
					return nil
				}
				for j := 0; j < widenReadsBetweenCheckAndCreate; j++ {
					if _, err := store.Nodes().FindLatestCascadePending(ctx, fix.NodeID, fix.MainRunScopeID, fix.FrameID, tx); err != nil {
						return err
					}
				}
				if _, err := store.Nodes().CreateCascadePending(ctx, fix.NodeID, fix.MainRunScopeID, fix.FrameID, tx); err != nil {
					return err
				}
				atomic.StoreInt32(&created[i], 1)
				return nil
			})
			if err != nil {
				errCh <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("LockReceiverCascade mutual-exclusion worker: %v", err)
	}

	var createdCount int
	for _, c := range created {
		createdCount += int(c)
	}
	if createdCount != 1 {
		t.Fatalf("LockReceiverCascade must serialize the find-then-create race so exactly one concurrent transaction creates the cascade-pending row for a given (node, run-scope, frame); got %d of %d", createdCount, workers)
	}
}

// @concept: cascade
func testSetRunRequiredClaimProducers_ReusesStaleRun(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()
	q := d.Queue()

	var runID shared.UUID
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		id, err := store.Nodes().CreateCascadePending(ctx, fix.NodeID, fix.MainRunScopeID, fix.FrameID, tx)
		if err != nil {
			return err
		}
		runID = id
		return store.Nodes().TransitionPendingToStale(ctx, id, time.Now().Add(-time.Second), tx)
	}); err != nil {
		t.Fatalf("seed stale run: %v", err)
	}

	countRoutable := func() int {
		n := 0
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			cands, err := q.SelectCandidates(ctx, persistence.SelectCandidatesRequest{
				Limit: 10,
			}, tx)
			if err != nil {
				return err
			}
			for _, c := range cands {
				if c.NodeID == fix.NodeID {
					n++
				}
			}
			return nil
		}); err != nil {
			t.Fatalf("SelectCandidates: %v", err)
		}
		return n
	}

	for i := 0; i < 3; i++ {
		var ok bool
		if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			var e error
			ok, e = store.Nodes().SetRunRequiredClaimProducers(ctx, runID, []string{"alpha", "beta"}, tx)
			return e
		}); err != nil {
			t.Fatalf("SetRunRequiredClaimProducers tick %d: %v", i, err)
		}
		if !ok {
			t.Fatalf("SetRunRequiredClaimProducers tick %d: want true for a stale unclaimed run", i)
		}
	}

	if got := countRoutable(); got != 1 {
		t.Fatalf("SetRunRequiredClaimProducers must update in place: want exactly 1 routable run after 3 ticks, got %d", got)
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		ok, err := q.ClaimDispatchRow(ctx, runID, "sup-a", tx)
		if err != nil {
			return err
		}
		if !ok {
			t.Fatalf("ClaimDispatchRow returned !ok")
		}
		return nil
	}); err != nil {
		t.Fatalf("claim: %v", err)
	}

	var ok bool
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var e error
		ok, e = store.Nodes().SetRunRequiredClaimProducers(ctx, runID, []string{"gamma"}, tx)
		return e
	}); err != nil {
		t.Fatalf("SetRunRequiredClaimProducers after claim: %v", err)
	}
	if ok {
		t.Fatalf("SetRunRequiredClaimProducers must not touch a claimed run")
	}
}

// @decision: non-cascade-direct-to-stale
func testCreateNonCascadeStaleCarriesForward(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	var priorRunID shared.UUID
	priorData := map[string]any{"marker": "from-prior-run"}
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if _, err := store.Nodes().CreateNonCascadeStale(ctx, persistence.NonCascadeStaleInput{
			NodeID:         fix.NodeID,
			RunScopeID:     fix.MainRunScopeID,
			FrameID:        fix.FrameID,
			ExecutorName:   "test-executor",
			EnqueuedAt:     time.Now().Add(-time.Minute),
			CreationReason: cascade.CreationReasonRecalculate,
		}, tx); err != nil {
			return err
		}
		latest, err := store.Nodes().GetLatestRunForNode(ctx, fix.NodeID, tx)
		if err != nil {
			return err
		}
		if latest == nil {
			t.Fatalf("CreateNonCascadeStale: no latest run found")
		}
		priorRunID = latest.NodeRunID
		return store.NodeAttributes().Upsert(ctx, latest.NodeRunID, fix.NodeID, priorData, tx)
	}); err != nil {
		t.Fatalf("seed prior run: %v", err)
	}

	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		return store.Nodes().UpdateState(ctx, priorRunID,
			cascade.NodeStateFresh, cascade.ReasonPureCascade, nil, tx)
	}); err != nil {
		t.Fatalf("settle prior run: %v", err)
	}

	var newRunID shared.UUID
	if err := store.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		id, err := store.Nodes().CreateNonCascadeStale(ctx, persistence.NonCascadeStaleInput{
			NodeID:         fix.NodeID,
			RunScopeID:     fix.MainRunScopeID,
			FrameID:        fix.FrameID,
			ExecutorName:   "test-executor",
			EnqueuedAt:     time.Now(),
			CreationReason: cascade.CreationReasonOperatorInvalidate,
		}, tx)
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
		snapshot, err := store.NodeAttributes().GetDispatchInputBag(ctx, newRunID, tx)
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
