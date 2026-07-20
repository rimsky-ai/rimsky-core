// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: wait-set
package conformance

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func testWaitSet(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	receiverID := uuid.New()
	senderAID := uuid.New()
	senderBID := uuid.New()
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if _, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: receiverID, InstanceID: fix.InstanceID,
			NodeType: "receiver", Executor: "test-executor",
		}, tx); err != nil {
			return err
		}
		if _, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: senderAID, InstanceID: fix.InstanceID,
			NodeType: "sender-a", Executor: "test-executor",
		}, tx); err != nil {
			return err
		}
		if _, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: senderBID, InstanceID: fix.InstanceID,
			NodeType: "sender-b", Executor: "test-executor",
		}, tx); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("seed nodes: %v", err)
	}
	receiverNodeRunID := seedConformanceRunForNode(ctx, t, d, receiverID, fix.FrameID)
	senderARunID := seedConformanceRunForNode(ctx, t, d, senderAID, fix.FrameID)
	senderBRunID := seedConformanceRunForNode(ctx, t, d, senderBID, fix.FrameID)

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if err := store.WaitSet().Insert(ctx, persistence.WaitSetRow{
			FrameID: fix.FrameID, ReceiverNodeRunID: receiverNodeRunID,
			SenderNodeRunID: senderARunID,
			TopicKind:       "state",
		}, tx); err != nil {
			return err
		}
		if err := store.WaitSet().Insert(ctx, persistence.WaitSetRow{
			FrameID: fix.FrameID, ReceiverNodeRunID: receiverNodeRunID,
			SenderNodeRunID: senderARunID,
			TopicKind:       "state",
		}, tx); err != nil {
			return err
		}
		filter, _ := json.Marshal(map[string]string{"name": "result"})
		return store.WaitSet().Insert(ctx, persistence.WaitSetRow{
			FrameID: fix.FrameID, ReceiverNodeRunID: receiverNodeRunID,
			SenderNodeRunID: senderBRunID,
			TopicKind:       "attribute",
			TopicFilter:     filter,
		}, tx)
	}); err != nil {
		t.Fatalf("wait_set insert: %v", err)
	}

	var byReceiver []persistence.WaitSetRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		rows, err := store.WaitSet().ListForReceiver(ctx, fix.FrameID, receiverNodeRunID, tx)
		byReceiver = rows
		return err
	}); err != nil {
		t.Fatalf("ListForReceiver: %v", err)
	}
	if len(byReceiver) != 2 {
		t.Fatalf("ListForReceiver: got %d rows want 2 (idempotent insert should not duplicate)", len(byReceiver))
	}
	for _, r := range byReceiver {
		if r.DrainedAt != nil {
			t.Fatalf("ListForReceiver: pre-drain row has DrainedAt != nil: %+v", r)
		}
	}

	var byFrame []persistence.WaitSetRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		rows, err := store.WaitSet().ListForFrame(ctx, fix.FrameID, tx)
		byFrame = rows
		return err
	}); err != nil {
		t.Fatalf("ListForFrame: %v", err)
	}
	if len(byFrame) != 2 {
		t.Fatalf("ListForFrame: got %d rows want 2", len(byFrame))
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.WaitSet().MarkDrainedBySender(ctx, fix.FrameID, senderARunID, tx)
	}); err != nil {
		t.Fatalf("MarkDrainedBySender: %v", err)
	}
	var afterDrainA []persistence.WaitSetRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		rows, err := store.WaitSet().ListForReceiver(ctx, fix.FrameID, receiverNodeRunID, tx)
		afterDrainA = rows
		return err
	}); err != nil {
		t.Fatalf("ListForReceiver after drain A: %v", err)
	}
	if len(afterDrainA) != 2 {
		t.Fatalf("after MarkDrainedBySender(senderA): got %d rows want 2 (drained rows must be retained)", len(afterDrainA))
	}
	var senderADrained, senderBDrained *persistence.WaitSetRow
	for i := range afterDrainA {
		switch afterDrainA[i].SenderNodeRunID {
		case senderARunID:
			senderADrained = &afterDrainA[i]
		case senderBRunID:
			senderBDrained = &afterDrainA[i]
		}
	}
	if senderADrained == nil {
		t.Fatalf("senderA row missing after drain")
	}
	if senderADrained.DrainedAt == nil {
		t.Fatalf("senderA row: DrainedAt nil after drain")
	}
	if senderBDrained == nil {
		t.Fatalf("senderB row missing after drain")
	}
	if senderBDrained.DrainedAt != nil {
		t.Fatalf("senderB row: DrainedAt non-nil before drain")
	}

	priorDrainedAt := *senderADrained.DrainedAt
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.WaitSet().MarkDrainedBySender(ctx, fix.FrameID, senderARunID, tx)
	}); err != nil {
		t.Fatalf("MarkDrainedBySender(senderA) re-run: %v", err)
	}
	var afterReRun []persistence.WaitSetRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		rows, err := store.WaitSet().ListForReceiver(ctx, fix.FrameID, receiverNodeRunID, tx)
		afterReRun = rows
		return err
	}); err != nil {
		t.Fatalf("ListForReceiver after re-run: %v", err)
	}
	for _, r := range afterReRun {
		if r.SenderNodeRunID != senderARunID {
			continue
		}
		if r.DrainedAt == nil || !r.DrainedAt.Equal(priorDrainedAt) {
			t.Fatalf("MarkDrainedBySender idempotency: senderA drained_at advanced from %v to %v", priorDrainedAt, r.DrainedAt)
		}
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.WaitSet().MarkDrainedBySender(ctx, fix.FrameID, senderBRunID, tx)
	}); err != nil {
		t.Fatalf("MarkDrainedBySender senderB: %v", err)
	}
	var allDrained []persistence.WaitSetRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		rows, err := store.WaitSet().ListForFrame(ctx, fix.FrameID, tx)
		allDrained = rows
		return err
	}); err != nil {
		t.Fatalf("ListForFrame after drain: %v", err)
	}
	if len(allDrained) != 2 {
		t.Fatalf("after final MarkDrainedBySender: got %d rows want 2 (rows are retained, not deleted)", len(allDrained))
	}
	for _, r := range allDrained {
		if r.DrainedAt == nil {
			t.Fatalf("row %v: DrainedAt nil after both drains", r)
		}
	}

	var drainedAttrs []persistence.WaitSetRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		rows, err := store.WaitSet().ListDrainedAttributeRowsForReceiver(ctx, fix.FrameID, receiverNodeRunID, tx)
		drainedAttrs = rows
		return err
	}); err != nil {
		t.Fatalf("ListDrainedAttributeRowsForReceiver: %v", err)
	}
	if len(drainedAttrs) != 1 {
		t.Fatalf("ListDrainedAttributeRowsForReceiver: got %d rows want 1 (only the attribute-topic row should match)", len(drainedAttrs))
	}
	if drainedAttrs[0].SenderNodeRunID != senderBRunID {
		t.Fatalf("drained-attr row sender=%v want %v", drainedAttrs[0].SenderNodeRunID, senderBRunID)
	}
	if drainedAttrs[0].TopicKind != "attribute" {
		t.Fatalf("drained-attr row topic=%q want attribute", drainedAttrs[0].TopicKind)
	}
}

func testWaitSetFrameIsolation(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fixA := seedFixtureSet(ctx, t, d)
	fixB := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	seedPair := func(fix fixtureSet) (receiverRunID, senderRunID shared.UUID) {
		receiverID := uuid.New()
		senderID := uuid.New()
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			if _, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
				ID: receiverID, InstanceID: fix.InstanceID,
				NodeType: "receiver", Executor: "test-executor",
			}, tx); err != nil {
				return err
			}
			_, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
				ID: senderID, InstanceID: fix.InstanceID,
				NodeType: "sender", Executor: "test-executor",
			}, tx)
			return err
		}); err != nil {
			t.Fatalf("seed nodes: %v", err)
		}
		receiverRunID = seedConformanceRunForNode(ctx, t, d, receiverID, fix.FrameID)
		senderRunID = seedConformanceRunForNode(ctx, t, d, senderID, fix.FrameID)
		return receiverRunID, senderRunID
	}

	receiverA, senderA := seedPair(fixA)
	receiverB, senderB := seedPair(fixB)

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if err := store.WaitSet().Insert(ctx, persistence.WaitSetRow{
			FrameID: fixA.FrameID, ReceiverNodeRunID: receiverA,
			SenderNodeRunID: senderA, TopicKind: "state",
		}, tx); err != nil {
			return err
		}
		return store.WaitSet().Insert(ctx, persistence.WaitSetRow{
			FrameID: fixB.FrameID, ReceiverNodeRunID: receiverB,
			SenderNodeRunID: senderB, TopicKind: "state",
		}, tx)
	}); err != nil {
		t.Fatalf("wait_set insert: %v", err)
	}

	var byFrameA, byFrameB []persistence.WaitSetRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		var err error
		byFrameA, err = store.WaitSet().ListForFrame(ctx, fixA.FrameID, tx)
		if err != nil {
			return err
		}
		byFrameB, err = store.WaitSet().ListForFrame(ctx, fixB.FrameID, tx)
		return err
	}); err != nil {
		t.Fatalf("ListForFrame: %v", err)
	}
	if len(byFrameA) != 1 || byFrameA[0].ReceiverNodeRunID != receiverA {
		t.Fatalf("ListForFrame(frameA) = %+v, want exactly frameA's row (receiver %v) — frameB's row must not leak in", byFrameA, receiverA)
	}
	if len(byFrameB) != 1 || byFrameB[0].ReceiverNodeRunID != receiverB {
		t.Fatalf("ListForFrame(frameB) = %+v, want exactly frameB's row (receiver %v) — frameA's row must not leak in", byFrameB, receiverB)
	}

	var receiverListA []persistence.WaitSetRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		var err error
		receiverListA, err = store.WaitSet().ListForReceiver(ctx, fixA.FrameID, receiverA, tx)
		return err
	}); err != nil {
		t.Fatalf("ListForReceiver: %v", err)
	}
	if len(receiverListA) != 1 {
		t.Fatalf("ListForReceiver(frameA, receiverA) = %+v, want 1 row", receiverListA)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.WaitSet().MarkDrainedBySender(ctx, fixB.FrameID, senderB, tx)
	}); err != nil {
		t.Fatalf("MarkDrainedBySender(frameB): %v", err)
	}

	var afterCrossDrain []persistence.WaitSetRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		var err error
		afterCrossDrain, err = store.WaitSet().ListForReceiver(ctx, fixA.FrameID, receiverA, tx)
		return err
	}); err != nil {
		t.Fatalf("ListForReceiver after cross-frame drain: %v", err)
	}
	if len(afterCrossDrain) != 1 || afterCrossDrain[0].DrainedAt != nil {
		t.Fatalf("MarkDrainedBySender(frameB, senderB) must not affect frameA's rows, got %+v", afterCrossDrain)
	}

	var drainedAttrsA []persistence.WaitSetRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		var err error
		drainedAttrsA, err = store.WaitSet().ListDrainedAttributeRowsForReceiver(ctx, fixA.FrameID, receiverA, tx)
		return err
	}); err != nil {
		t.Fatalf("ListDrainedAttributeRowsForReceiver: %v", err)
	}
	if len(drainedAttrsA) != 0 {
		t.Fatalf("ListDrainedAttributeRowsForReceiver(frameA, receiverA) = %+v, want empty — frameB drain must not leak in", drainedAttrsA)
	}
}

func testWaitSetTopicKindDistinctness(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	receiverID := uuid.New()
	senderID := uuid.New()
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if _, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: receiverID, InstanceID: fix.InstanceID,
			NodeType: "receiver", Executor: "test-executor",
		}, tx); err != nil {
			return err
		}
		_, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: senderID, InstanceID: fix.InstanceID,
			NodeType: "sender", Executor: "test-executor",
		}, tx)
		return err
	}); err != nil {
		t.Fatalf("seed nodes: %v", err)
	}
	receiverNodeRunID := seedConformanceRunForNode(ctx, t, d, receiverID, fix.FrameID)
	senderRunID := seedConformanceRunForNode(ctx, t, d, senderID, fix.FrameID)

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.WaitSet().Insert(ctx, persistence.WaitSetRow{
			FrameID: fix.FrameID, ReceiverNodeRunID: receiverNodeRunID,
			SenderNodeRunID: senderRunID,
			TopicKind:       "state",
		}, tx)
	}); err != nil {
		t.Fatalf("insert (frame, receiver, sender, state): %v", err)
	}

	var afterState []persistence.WaitSetRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		rows, err := store.WaitSet().ListForReceiver(ctx, fix.FrameID, receiverNodeRunID, tx)
		afterState = rows
		return err
	}); err != nil {
		t.Fatalf("ListForReceiver after state insert: %v", err)
	}
	if len(afterState) != 1 {
		t.Fatalf("ListForReceiver after (frame,receiver,sender,state): got %d rows want 1", len(afterState))
	}

	filter, _ := json.Marshal(map[string]string{"name": "result"})
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.WaitSet().Insert(ctx, persistence.WaitSetRow{
			FrameID: fix.FrameID, ReceiverNodeRunID: receiverNodeRunID,
			SenderNodeRunID: senderRunID,
			TopicKind:       "attribute",
			TopicFilter:     filter,
		}, tx)
	}); err != nil {
		t.Fatalf("insert (frame, receiver, sender, attribute): %v", err)
	}

	var afterAttribute []persistence.WaitSetRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		rows, err := store.WaitSet().ListForReceiver(ctx, fix.FrameID, receiverNodeRunID, tx)
		afterAttribute = rows
		return err
	}); err != nil {
		t.Fatalf("ListForReceiver after attribute insert: %v", err)
	}
	if len(afterAttribute) != 2 {
		t.Fatalf("ListForReceiver after same (frame,receiver,sender) with a second topic_kind: got %d rows want 2 (topic_kind must be part of the wait-set key)", len(afterAttribute))
	}
	kinds := map[string]bool{}
	for _, r := range afterAttribute {
		if r.SenderNodeRunID != senderRunID {
			t.Fatalf("ListForReceiver: unexpected sender %v", r.SenderNodeRunID)
		}
		kinds[r.TopicKind] = true
	}
	if !kinds["state"] || !kinds["attribute"] {
		t.Fatalf("ListForReceiver: want distinct rows for topic_kind state and attribute, got %v", afterAttribute)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.WaitSet().Insert(ctx, persistence.WaitSetRow{
			FrameID: fix.FrameID, ReceiverNodeRunID: receiverNodeRunID,
			SenderNodeRunID: senderRunID,
			TopicKind:       "state",
		}, tx)
	}); err != nil {
		t.Fatalf("re-insert (frame, receiver, sender, state): %v", err)
	}
	var afterReinsert []persistence.WaitSetRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		rows, err := store.WaitSet().ListForReceiver(ctx, fix.FrameID, receiverNodeRunID, tx)
		afterReinsert = rows
		return err
	}); err != nil {
		t.Fatalf("ListForReceiver after re-insert: %v", err)
	}
	if len(afterReinsert) != 2 {
		t.Fatalf("ListForReceiver after re-inserting an existing (frame,receiver,sender,topic_kind): got %d rows want 2 (per-topic_kind dedupe must still hold)", len(afterReinsert))
	}
}

// @concept: cascade
// @decision: walker-rule-per-sender-node
func testWaitSetGateEvaluatorMethods(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	pendingReceiverID := uuid.New()
	staleReceiverID := uuid.New()
	senderAID := uuid.New()
	senderBID := uuid.New()
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		for _, n := range []struct {
			id       uuid.UUID
			nodeType string
		}{
			{pendingReceiverID, "gate-pending-receiver"},
			{staleReceiverID, "gate-stale-receiver"},
			{senderAID, "gate-sender-a"},
			{senderBID, "gate-sender-b"},
		} {
			if _, err := store.Nodes().Create(ctx, persistence.NodeCreateInput{
				ID: n.id, InstanceID: fix.InstanceID,
				NodeType: n.nodeType, Executor: "test-executor",
			}, tx); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed nodes: %v", err)
	}

	var pendingReceiverRunID shared.UUID
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		id, err := store.Nodes().CreateCascadePending(ctx, tx, pendingReceiverID, fix.MainRunScopeID, fix.FrameID)
		pendingReceiverRunID = id
		return err
	}); err != nil {
		t.Fatalf("CreateCascadePending pending receiver: %v", err)
	}
	staleReceiverRunID := seedConformanceRunForNode(ctx, t, d, staleReceiverID, fix.FrameID)
	senderARunID := seedConformanceRunForNode(ctx, t, d, senderAID, fix.FrameID)
	senderBRunID := seedConformanceRunForNode(ctx, t, d, senderBID, fix.FrameID)

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if err := store.WaitSet().Insert(ctx, persistence.WaitSetRow{
			FrameID: fix.FrameID, ReceiverNodeRunID: pendingReceiverRunID,
			SenderNodeRunID: senderARunID, TopicKind: "state",
		}, tx); err != nil {
			return err
		}
		if err := store.WaitSet().Insert(ctx, persistence.WaitSetRow{
			FrameID: fix.FrameID, ReceiverNodeRunID: pendingReceiverRunID,
			SenderNodeRunID: senderBRunID, TopicKind: "state",
		}, tx); err != nil {
			return err
		}
		return store.WaitSet().Insert(ctx, persistence.WaitSetRow{
			FrameID: fix.FrameID, ReceiverNodeRunID: staleReceiverRunID,
			SenderNodeRunID: senderARunID, TopicKind: "state",
		}, tx)
	}); err != nil {
		t.Fatalf("wait_set insert: %v", err)
	}

	var senderNodes []shared.UUID
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		rows, err := store.WaitSet().ListSenderNodesForReceiver(ctx, fix.FrameID, pendingReceiverRunID, tx)
		senderNodes = rows
		return err
	}); err != nil {
		t.Fatalf("ListSenderNodesForReceiver: %v", err)
	}
	gotSenders := map[shared.UUID]bool{}
	for _, id := range senderNodes {
		gotSenders[id] = true
	}
	if len(gotSenders) != 2 || !gotSenders[shared.UUID(senderAID)] || !gotSenders[shared.UUID(senderBID)] {
		t.Fatalf("ListSenderNodesForReceiver: got %v want {%s,%s}", senderNodes, senderAID, senderBID)
	}

	hasRow := func(receiverRunID, senderRunID shared.UUID) bool {
		var got bool
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			v, err := store.WaitSet().HasRowForSenderRun(ctx, fix.FrameID, receiverRunID, senderRunID, tx)
			got = v
			return err
		}); err != nil {
			t.Fatalf("HasRowForSenderRun: %v", err)
		}
		return got
	}
	if !hasRow(pendingReceiverRunID, senderARunID) {
		t.Fatalf("HasRowForSenderRun: want true for (pendingReceiver, senderA)")
	}
	if !hasRow(pendingReceiverRunID, senderBRunID) {
		t.Fatalf("HasRowForSenderRun: want true for (pendingReceiver, senderB)")
	}
	if hasRow(staleReceiverRunID, senderBRunID) {
		t.Fatalf("HasRowForSenderRun: want false for (staleReceiver, senderB) - no row inserted")
	}
	if hasRow(pendingReceiverRunID, uuid.New()) {
		t.Fatalf("HasRowForSenderRun: want false for a sender run that was never inserted")
	}

	hasUndrained := func(receiverRunID shared.UUID) bool {
		var got bool
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			v, err := store.WaitSet().HasUndrainedRowsForReceiver(ctx, fix.FrameID, receiverRunID, tx)
			got = v
			return err
		}); err != nil {
			t.Fatalf("HasUndrainedRowsForReceiver: %v", err)
		}
		return got
	}
	if !hasUndrained(pendingReceiverRunID) {
		t.Fatalf("HasUndrainedRowsForReceiver: want true before any drain")
	}

	pendingReceiversFor := func(senderRunID shared.UUID) []shared.UUID {
		var got []shared.UUID
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			rows, err := store.WaitSet().ListPendingReceiversForDrainedSender(ctx, fix.FrameID, senderRunID, tx)
			got = rows
			return err
		}); err != nil {
			t.Fatalf("ListPendingReceiversForDrainedSender: %v", err)
		}
		return got
	}
	containsUUID := func(list []shared.UUID, want shared.UUID) bool {
		for _, id := range list {
			if id == want {
				return true
			}
		}
		return false
	}

	beforeDrain := pendingReceiversFor(senderARunID)
	if !containsUUID(beforeDrain, pendingReceiverRunID) {
		t.Fatalf("ListPendingReceiversForDrainedSender(senderA) before drain: want pendingReceiverRunID present, got %v", beforeDrain)
	}
	if containsUUID(beforeDrain, staleReceiverRunID) {
		t.Fatalf("ListPendingReceiversForDrainedSender(senderA): a receiver run whose state != pending/cascade must never be surfaced, got %v", beforeDrain)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.WaitSet().MarkDrainedBySender(ctx, fix.FrameID, senderARunID, tx)
	}); err != nil {
		t.Fatalf("MarkDrainedBySender senderA: %v", err)
	}
	if !hasUndrained(pendingReceiverRunID) {
		t.Fatalf("HasUndrainedRowsForReceiver: want true after draining only senderA (senderB row still undrained)")
	}
	afterDrainA := pendingReceiversFor(senderARunID)
	if !containsUUID(afterDrainA, pendingReceiverRunID) {
		t.Fatalf("ListPendingReceiversForDrainedSender(senderA) after drain: want pendingReceiverRunID present, got %v", afterDrainA)
	}
	if got := pendingReceiversFor(uuid.New()); len(got) != 0 {
		t.Fatalf("ListPendingReceiversForDrainedSender: want empty for a sender run with no wait_set rows, got %v", got)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.WaitSet().MarkDrainedBySender(ctx, fix.FrameID, senderBRunID, tx)
	}); err != nil {
		t.Fatalf("MarkDrainedBySender senderB: %v", err)
	}
	if hasUndrained(pendingReceiverRunID) {
		t.Fatalf("HasUndrainedRowsForReceiver: want false once every sender row for the receiver is drained")
	}
	afterDrainB := pendingReceiversFor(senderBRunID)
	if !containsUUID(afterDrainB, pendingReceiverRunID) {
		t.Fatalf("ListPendingReceiversForDrainedSender(senderB) after drain: want pendingReceiverRunID present, got %v", afterDrainB)
	}
}
