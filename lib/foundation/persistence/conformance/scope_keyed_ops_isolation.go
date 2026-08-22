// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: run-scope

package conformance

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func resolveMostRecentRun(ctx context.Context, t *testing.T, d persistence.Database, nodeID, runScopeID shared.UUID) shared.UUID {
	t.Helper()
	store := d.Tables()
	q := d.Queue()
	var id shared.UUID
	var found bool
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		var err error
		id, found, err = q.GetMostRecentRunForNodeInScope(ctx, nodeID, runScopeID, tx)
		return err
	}); err != nil {
		t.Fatalf("resolveMostRecentRun: %v", err)
	}
	if !found {
		t.Fatalf("resolveMostRecentRun: no run for node %s in scope %s", nodeID, runScopeID)
	}
	return id
}

func testScopeKeyedOps_GetMostRecentRunForNodeInScope(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	f := seedTwoScopeRuns(ctx, t, d)
	store := d.Tables()
	q := d.Queue()

	var gotA, gotB shared.UUID
	var foundA, foundB bool
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		var err error
		gotA, foundA, err = q.GetMostRecentRunForNodeInScope(ctx, f.fix.NodeID, f.scopeA, tx)
		if err != nil {
			return err
		}
		gotB, foundB, err = q.GetMostRecentRunForNodeInScope(ctx, f.fix.NodeID, f.scopeB, tx)
		return err
	}); err != nil {
		t.Fatalf("GetMostRecentRunForNodeInScope: %v", err)
	}
	if !foundA || gotA != f.runA {
		t.Fatalf("GetMostRecentRunForNodeInScope(scopeA) = %v, found=%v, want %v", gotA, foundA, f.runA)
	}
	if !foundB || gotB != f.runB {
		t.Fatalf("GetMostRecentRunForNodeInScope(scopeB) = %v, found=%v, want %v", gotB, foundB, f.runB)
	}
}

func testScopeKeyedOps_HasAdvancedSiblingInScope(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	f := seedTwoScopeRuns(ctx, t, d)
	store := d.Tables()

	var advancedInA bool
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		var err error
		advancedInA, err = store.Nodes().HasAdvancedSiblingInScope(ctx, f.fix.NodeID, f.scopeA, f.runA, tx)
		return err
	}); err != nil {
		t.Fatalf("HasAdvancedSiblingInScope(scopeA): %v", err)
	}
	if advancedInA {
		t.Fatalf("HasAdvancedSiblingInScope(scopeA, excluding runA) = true; scopeB's runB leaked in as a sibling")
	}
}

// @concept: cascade-mode
// @concept: wait-set
// @story: sequenced-preserves-cascade-rounds
func testScopeKeyedOps_HasEarlierQueuedRoundFromSameSender(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	f := seedTwoScopeRuns(ctx, t, d)
	store := d.Tables()

	newSenderNode := func(nodeType string) shared.UUID {
		t.Helper()
		id := shared.UUID(uuid.New())
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			_, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
				ID: id, InstanceID: f.fix.InstanceID, NodeType: nodeType, Executor: "test-executor",
			}, tx)
			return err
		}); err != nil {
			t.Fatalf("create sender node %s: %v", nodeType, err)
		}
		return id
	}
	newRound := func(nodeID, scopeID shared.UUID) shared.UUID {
		t.Helper()
		var id shared.UUID
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			var err error
			id, err = store.Nodes().CreateCascadePending(ctx, nodeID, scopeID, f.fix.FrameID, tx)
			return err
		}); err != nil {
			t.Fatalf("create cascade round: %v", err)
		}
		return id
	}
	nameSender := func(receiverRunID, senderRunID shared.UUID) {
		t.Helper()
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			return store.WaitSet().Insert(ctx, persistence.WaitSetRow{
				FrameID:           f.fix.FrameID,
				ReceiverNodeRunID: receiverRunID,
				SenderNodeRunID:   senderRunID,
				TopicKind:         "terminal",
			}, tx)
		}); err != nil {
			t.Fatalf("insert wait-set row: %v", err)
		}
	}
	ask := func(receiverRunID shared.UUID) bool {
		t.Helper()
		var got bool
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			var err error
			got, err = store.Nodes().HasEarlierQueuedRoundFromSameSender(ctx, receiverRunID, tx)
			return err
		}); err != nil {
			t.Fatalf("HasEarlierQueuedRoundFromSameSender: %v", err)
		}
		return got
	}
	move := func(runID shared.UUID, steps ...struct {
		state  cascade.NodeState
		reason cascade.TransitionReason
	}) {
		t.Helper()
		for _, step := range steps {
			if err := inTx(ctx, store, func(tx persistence.Tx) error {
				return store.Nodes().UpdateState(ctx, runID, step.state, step.reason, nil, tx)
			}); err != nil {
				t.Fatalf("move run %s to %s: %v", runID, step.state, err)
			}
		}
	}
	step := func(state cascade.NodeState, reason cascade.TransitionReason) struct {
		state  cascade.NodeState
		reason cascade.TransitionReason
	} {
		return struct {
			state  cascade.NodeState
			reason cascade.TransitionReason
		}{state, reason}
	}

	senderOne := newSenderNode("sender-one")
	senderTwo := newSenderNode("sender-two")
	senderOneRunA := newRound(senderOne, f.scopeA)
	senderOneRunB := newRound(senderOne, f.scopeA)
	senderTwoRun := newRound(senderTwo, f.scopeA)

	first := newRound(f.fix.NodeID, f.scopeA)
	nameSender(first, senderOneRunA)
	second := newRound(f.fix.NodeID, f.scopeA)
	nameSender(second, senderOneRunB)
	fromTheOtherSender := newRound(f.fix.NodeID, f.scopeA)
	nameSender(fromTheOtherSender, senderTwoRun)
	third := newRound(f.fix.NodeID, f.scopeA)
	nameSender(third, senderOneRunA)

	otherScope := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.RunScopes().Create(ctx, persistence.RunScopeRow{
			ID:               otherScope,
			ParentRunScopeID: &f.scopeA,
			ParentNodeRunID:  &f.runA,
			GraphName:        spec.MainGraphName,
			InstanceID:       f.fix.InstanceID,
			PartitionKey:     "scope-other",
		}, tx)
	}); err != nil {
		t.Fatalf("create the other run scope: %v", err)
	}
	onlyRoundElsewhere := newRound(f.fix.NodeID, otherScope)
	nameSender(onlyRoundElsewhere, senderOneRunA)

	if ask(first) {
		t.Fatal("the sender's first round found a predecessor")
	}
	if !ask(second) {
		t.Fatal("a round missed the older queued round of the same sender")
	}
	if !ask(third) {
		t.Fatal("a round missed the two older queued rounds of the same sender")
	}
	if ask(fromTheOtherSender) {
		t.Fatal("a round waited behind another sender's queued rounds; sequenced orders a receiver's " +
			"rounds per sender, not across senders")
	}
	if ask(onlyRoundElsewhere) {
		t.Fatal("the probe read another run scope's queued rounds")
	}

	move(first,
		step(cascade.NodeStateStale, cascade.ReasonGateCleared),
		step(cascade.NodeStateFresh, cascade.ReasonPureCascade))
	move(second,
		step(cascade.NodeStateStale, cascade.ReasonGateCleared),
		step(cascade.NodeStateRunning, cascade.ReasonDispatchClaimed),
		step(cascade.NodeStateParked, cascade.ReasonHandlerPark))
	if ask(third) {
		t.Fatal("a settled round and a parked round still held their successor; " +
			"only a queued round holds one back")
	}
}

func testScopeKeyedOps_ListPendingRunsInScopeForNodes(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	f := seedTwoScopeRuns(ctx, t, d)
	store := d.Tables()

	var pendingA, pendingB shared.UUID
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		var err error
		pendingA, err = store.Nodes().CreateCascadePending(ctx, f.fix.NodeID, f.scopeA, f.fix.FrameID, tx)
		if err != nil {
			return err
		}
		pendingB, err = store.Nodes().CreateCascadePending(ctx, f.fix.NodeID, f.scopeB, f.fix.FrameID, tx)
		return err
	}); err != nil {
		t.Fatalf("seed pendings: %v", err)
	}

	var listA, listB []shared.UUID
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		var err error
		listA, err = store.Nodes().ListPendingRunsInScopeForNodes(ctx, f.scopeA, []shared.UUID{f.fix.NodeID}, tx)
		if err != nil {
			return err
		}
		listB, err = store.Nodes().ListPendingRunsInScopeForNodes(ctx, f.scopeB, []shared.UUID{f.fix.NodeID}, tx)
		return err
	}); err != nil {
		t.Fatalf("ListPendingRunsInScopeForNodes: %v", err)
	}
	if len(listA) != 1 || listA[0] != pendingA {
		t.Fatalf("ListPendingRunsInScopeForNodes(scopeA) = %v, want [%v]", listA, pendingA)
	}
	if len(listB) != 1 || listB[0] != pendingB {
		t.Fatalf("ListPendingRunsInScopeForNodes(scopeB) = %v, want [%v]", listB, pendingB)
	}
}

func testScopeKeyedOps_HasLaterCascadePending(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	f := seedTwoScopeRuns(ctx, t, d)
	store := d.Tables()

	for i := 0; i < 4; i++ {
		seedConformanceRunForScope(ctx, t, d, f.fix.NodeID, f.fix.FrameID, f.scopeB)
	}

	var laterInA bool
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		row, err := store.Nodes().GetRunForGate(ctx, f.runA, tx)
		if err != nil {
			return err
		}
		if row == nil {
			t.Fatalf("GetRunForGate(runA) returned nil")
		}
		laterInA, err = store.Nodes().HasLaterCascadePending(ctx, f.fix.NodeID, f.scopeA, row.Sequence, tx)
		return err
	}); err != nil {
		t.Fatalf("HasLaterCascadePending(scopeA): %v", err)
	}
	if laterInA {
		t.Fatalf("HasLaterCascadePending(scopeA) = true; scopeB's higher-sequence rows leaked in")
	}
}

func testScopeKeyedOps_GetPriorRunBySequence(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	f := seedTwoScopeRuns(ctx, t, d)
	store := d.Tables()

	seedConformanceRunForScope(ctx, t, d, f.fix.NodeID, f.fix.FrameID, f.scopeA)
	seedConformanceRunForScope(ctx, t, d, f.fix.NodeID, f.fix.FrameID, f.scopeB)
	currentA := resolveMostRecentRun(ctx, t, d, f.fix.NodeID, f.scopeA)
	currentB := resolveMostRecentRun(ctx, t, d, f.fix.NodeID, f.scopeB)

	var priorA, priorB *persistence.NodeRunForGate
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		curA, err := store.Nodes().GetRunForGate(ctx, currentA, tx)
		if err != nil {
			return err
		}
		curB, err := store.Nodes().GetRunForGate(ctx, currentB, tx)
		if err != nil {
			return err
		}
		priorA, err = store.Nodes().GetPriorRunBySequence(ctx, f.fix.NodeID, f.scopeA, curA.Sequence, tx)
		if err != nil {
			return err
		}
		priorB, err = store.Nodes().GetPriorRunBySequence(ctx, f.fix.NodeID, f.scopeB, curB.Sequence, tx)
		return err
	}); err != nil {
		t.Fatalf("GetPriorRunBySequence: %v", err)
	}
	if priorA == nil || priorA.NodeRunID != f.runA {
		t.Fatalf("GetPriorRunBySequence(scopeA) = %+v, want runA %v", priorA, f.runA)
	}
	if priorB == nil || priorB.NodeRunID != f.runB {
		t.Fatalf("GetPriorRunBySequence(scopeB) = %+v, want runB %v", priorB, f.runB)
	}
}

func testScopeKeyedOps_DeletePriorCascadeStales(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	f := seedTwoScopeRuns(ctx, t, d)
	store := d.Tables()

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return forceRunStateToFresh(ctx, store, f.runA, tx)
	}); err != nil {
		t.Fatalf("settle runA (scopeB's structural parent): %v", err)
	}

	seedConformanceRunForScope(ctx, t, d, f.fix.NodeID, f.fix.FrameID, f.scopeA)
	staleA := resolveMostRecentRun(ctx, t, d, f.fix.NodeID, f.scopeA)
	seedConformanceRunForScope(ctx, t, d, f.fix.NodeID, f.fix.FrameID, f.scopeA)
	currentA := resolveMostRecentRun(ctx, t, d, f.fix.NodeID, f.scopeA)

	var deleted int
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		curA, err := store.Nodes().GetRunForGate(ctx, currentA, tx)
		if err != nil {
			return err
		}
		deleted, err = store.Nodes().DeletePriorCascadeStales(ctx, f.fix.NodeID, f.scopeA, curA.Sequence, tx)
		return err
	}); err != nil {
		t.Fatalf("DeletePriorCascadeStales(scopeA): %v", err)
	}
	if deleted != 1 {
		t.Fatalf("DeletePriorCascadeStales(scopeA) deleted %d rows, want 1 (staleA only)", deleted)
	}

	var staleAGone bool
	var runARow, runBRow *persistence.NodeRunForGate
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		row, err := store.Nodes().GetRunForGate(ctx, staleA, tx)
		if err != nil {
			return err
		}
		staleAGone = row == nil
		runARow, err = store.Nodes().GetRunForGate(ctx, f.runA, tx)
		if err != nil {
			return err
		}
		runBRow, err = store.Nodes().GetRunForGate(ctx, f.runB, tx)
		return err
	}); err != nil {
		t.Fatalf("post-delete probe: %v", err)
	}
	if !staleAGone {
		t.Fatalf("DeletePriorCascadeStales(scopeA) did not delete the prior stale row")
	}
	if runARow == nil {
		t.Fatalf("DeletePriorCascadeStales(scopeA) deleted runA, which is settled (not stale) and must be preserved")
	}
	if runBRow == nil {
		t.Fatalf("DeletePriorCascadeStales(scopeA) deleted scopeB's runB too — cross-scope leak")
	}
}

func testScopeKeyedOps_GetPriorCascadeQueuedNotClaimed(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	f := seedTwoScopeRuns(ctx, t, d)
	store := d.Tables()

	seedConformanceRunForScope(ctx, t, d, f.fix.NodeID, f.fix.FrameID, f.scopeA)
	seedConformanceRunForScope(ctx, t, d, f.fix.NodeID, f.fix.FrameID, f.scopeB)
	currentA := resolveMostRecentRun(ctx, t, d, f.fix.NodeID, f.scopeA)
	currentB := resolveMostRecentRun(ctx, t, d, f.fix.NodeID, f.scopeB)

	var priorA, priorB *persistence.NodeRunForGate
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		curA, err := store.Nodes().GetRunForGate(ctx, currentA, tx)
		if err != nil {
			return err
		}
		curB, err := store.Nodes().GetRunForGate(ctx, currentB, tx)
		if err != nil {
			return err
		}
		priorA, err = store.Nodes().GetPriorCascadeQueuedNotClaimed(ctx, f.fix.NodeID, f.scopeA, curA.Sequence, tx)
		if err != nil {
			return err
		}
		priorB, err = store.Nodes().GetPriorCascadeQueuedNotClaimed(ctx, f.fix.NodeID, f.scopeB, curB.Sequence, tx)
		return err
	}); err != nil {
		t.Fatalf("GetPriorCascadeQueuedNotClaimed: %v", err)
	}
	if priorA == nil || priorA.NodeRunID != f.runA {
		t.Fatalf("GetPriorCascadeQueuedNotClaimed(scopeA) = %+v, want runA %v", priorA, f.runA)
	}
	if priorB == nil || priorB.NodeRunID != f.runB {
		t.Fatalf("GetPriorCascadeQueuedNotClaimed(scopeB) = %+v, want runB %v", priorB, f.runB)
	}
}

func testScopeKeyedOps_GetMostRecentSettledRun(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	f := seedTwoScopeRuns(ctx, t, d)
	store := d.Tables()

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if err := forceRunStateToFresh(ctx, store, f.runA, tx); err != nil {
			return err
		}
		return forceRunStateToFresh(ctx, store, f.runB, tx)
	}); err != nil {
		t.Fatalf("settle runA/runB: %v", err)
	}

	seedConformanceRunForScope(ctx, t, d, f.fix.NodeID, f.fix.FrameID, f.scopeA)
	seedConformanceRunForScope(ctx, t, d, f.fix.NodeID, f.fix.FrameID, f.scopeB)
	currentA := resolveMostRecentRun(ctx, t, d, f.fix.NodeID, f.scopeA)
	currentB := resolveMostRecentRun(ctx, t, d, f.fix.NodeID, f.scopeB)

	var settledA, settledB *persistence.NodeRunForGate
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		curA, err := store.Nodes().GetRunForGate(ctx, currentA, tx)
		if err != nil {
			return err
		}
		curB, err := store.Nodes().GetRunForGate(ctx, currentB, tx)
		if err != nil {
			return err
		}
		settledA, err = store.Nodes().GetMostRecentSettledRun(ctx, f.fix.NodeID, f.scopeA, curA.Sequence, tx)
		if err != nil {
			return err
		}
		settledB, err = store.Nodes().GetMostRecentSettledRun(ctx, f.fix.NodeID, f.scopeB, curB.Sequence, tx)
		return err
	}); err != nil {
		t.Fatalf("GetMostRecentSettledRun: %v", err)
	}
	if settledA == nil || settledA.NodeRunID != f.runA {
		t.Fatalf("GetMostRecentSettledRun(scopeA) = %+v, want runA %v", settledA, f.runA)
	}
	if settledB == nil || settledB.NodeRunID != f.runB {
		t.Fatalf("GetMostRecentSettledRun(scopeB) = %+v, want runB %v", settledB, f.runB)
	}
}

func testScopeKeyedOps_SnapshotBagForNewRun(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	f := seedTwoScopeRuns(ctx, t, d)
	store := d.Tables()

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if err := store.NodeAttributes().MergeDelta(ctx, f.runA, map[string]any{"scope": "A"}, tx); err != nil {
			return err
		}
		return store.NodeAttributes().MergeDelta(ctx, f.runB, map[string]any{"scope": "B"}, tx)
	}); err != nil {
		t.Fatalf("seed attribute data: %v", err)
	}

	seedConformanceRunForScope(ctx, t, d, f.fix.NodeID, f.fix.FrameID, f.scopeA)
	seedConformanceRunForScope(ctx, t, d, f.fix.NodeID, f.fix.FrameID, f.scopeB)
	newRunA := resolveMostRecentRun(ctx, t, d, f.fix.NodeID, f.scopeA)
	newRunB := resolveMostRecentRun(ctx, t, d, f.fix.NodeID, f.scopeB)

	var bagA, bagB *persistence.NodeAttributesRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		var err error
		bagA, err = store.NodeAttributes().GetByRun(ctx, newRunA, tx)
		if err != nil {
			return err
		}
		bagB, err = store.NodeAttributes().GetByRun(ctx, newRunB, tx)
		return err
	}); err != nil {
		t.Fatalf("read carried-forward bags: %v", err)
	}
	if bagA == nil || bagA.Data["scope"] != "A" {
		t.Fatalf("SnapshotBagForNewRun(scopeA) carried forward %#v, want scope=A", bagA)
	}
	if bagB == nil || bagB.Data["scope"] != "B" {
		t.Fatalf("SnapshotBagForNewRun(scopeB) carried forward %#v, want scope=B", bagB)
	}
}
