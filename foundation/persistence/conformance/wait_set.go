// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// wait_set.go — WaitSetTable conformance fixture. Exercises the methods
// against both drivers (postgres + sqlite). See the subscription-cascade
// spec at .ok-planner/specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md
// and the per-run attribute pull spec at
// .ok-planner/specs/2026-05-20-attribute-pull-resolution-design.md for
// the mark-don't-delete-on-drain semantics.
//
//	@concept: wait-set
package conformance

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
)

func testWaitSet(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	// Seed three additional nodes so we have a (receiver, sender) pair plus
	// a third unrelated sender. Post-stage-5, rimsky_wait_set is keyed by
	// run id, so each node also gets a pending run row enqueued via the
	// per-area helper.
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
	receiverRunID := seedConformanceRunForNode(ctx, t, d, receiverID, fix.FrameID)
	senderARunID := seedConformanceRunForNode(ctx, t, d, senderAID, fix.FrameID)
	senderBRunID := seedConformanceRunForNode(ctx, t, d, senderBID, fix.FrameID)

	// Insert wait-set rows: receiver waits on senderA (state, direct) and
	// senderB (attribute, instance with filter).
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if err := store.WaitSet().Insert(ctx, persistence.WaitSetRow{
			FrameID: fix.FrameID, ReceiverRunID: receiverRunID,
			SenderRunID: senderARunID,
			TopicKind:   "state", SubscriptionScope: "direct",
		}, tx); err != nil {
			return err
		}
		// Duplicate insert is idempotent.
		if err := store.WaitSet().Insert(ctx, persistence.WaitSetRow{
			FrameID: fix.FrameID, ReceiverRunID: receiverRunID,
			SenderRunID: senderARunID,
			TopicKind:   "state", SubscriptionScope: "direct",
		}, tx); err != nil {
			return err
		}
		filter, _ := json.Marshal(map[string]string{"name": "result"})
		return store.WaitSet().Insert(ctx, persistence.WaitSetRow{
			FrameID: fix.FrameID, ReceiverRunID: receiverRunID,
			SenderRunID: senderBRunID,
			TopicKind:   "attribute", SubscriptionScope: "instance",
			TopicFilter: filter,
		}, tx)
	}); err != nil {
		t.Fatalf("wait_set insert: %v", err)
	}

	// ListForReceiver returns both rows.
	var byReceiver []persistence.WaitSetRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		rows, err := store.WaitSet().ListForReceiver(ctx, fix.FrameID, receiverRunID, tx)
		byReceiver = rows
		return err
	}); err != nil {
		t.Fatalf("ListForReceiver: %v", err)
	}
	if len(byReceiver) != 2 {
		t.Fatalf("ListForReceiver: got %d rows want 2 (idempotent insert should not duplicate)", len(byReceiver))
	}
	// Before draining, both rows should have DrainedAt == nil.
	for _, r := range byReceiver {
		if r.DrainedAt != nil {
			t.Fatalf("ListForReceiver: pre-drain row has DrainedAt != nil: %+v", r)
		}
	}

	// ListForFrame returns the same two rows.
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

	// MarkDrainedBySender(senderA) marks the state-direct row drained
	// but retains it (the row's DrainedAt becomes non-nil).
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.WaitSet().MarkDrainedBySender(ctx, fix.FrameID, senderARunID, tx)
	}); err != nil {
		t.Fatalf("MarkDrainedBySender: %v", err)
	}
	var afterDrainA []persistence.WaitSetRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		rows, err := store.WaitSet().ListForReceiver(ctx, fix.FrameID, receiverRunID, tx)
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
		switch afterDrainA[i].SenderRunID {
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

	// Idempotency: re-marking does not advance senderA's drained_at.
	priorDrainedAt := *senderADrained.DrainedAt
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.WaitSet().MarkDrainedBySender(ctx, fix.FrameID, senderARunID, tx)
	}); err != nil {
		t.Fatalf("MarkDrainedBySender(senderA) re-run: %v", err)
	}
	var afterReRun []persistence.WaitSetRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		rows, err := store.WaitSet().ListForReceiver(ctx, fix.FrameID, receiverRunID, tx)
		afterReRun = rows
		return err
	}); err != nil {
		t.Fatalf("ListForReceiver after re-run: %v", err)
	}
	for _, r := range afterReRun {
		if r.SenderRunID != senderARunID {
			continue
		}
		if r.DrainedAt == nil || !r.DrainedAt.Equal(priorDrainedAt) {
			t.Fatalf("MarkDrainedBySender idempotency: senderA drained_at advanced from %v to %v", priorDrainedAt, r.DrainedAt)
		}
	}

	// MarkDrainedBySender(senderB) drains the remainder.
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

	// ListDrainedAttributeRowsForReceiver: should return only the
	// attribute-topic row (senderB), since senderA's row is state-kind.
	var drainedAttrs []persistence.WaitSetRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		rows, err := store.WaitSet().ListDrainedAttributeRowsForReceiver(ctx, fix.FrameID, receiverRunID, tx)
		drainedAttrs = rows
		return err
	}); err != nil {
		t.Fatalf("ListDrainedAttributeRowsForReceiver: %v", err)
	}
	if len(drainedAttrs) != 1 {
		t.Fatalf("ListDrainedAttributeRowsForReceiver: got %d rows want 1 (only the attribute-topic row should match)", len(drainedAttrs))
	}
	if drainedAttrs[0].SenderRunID != senderBRunID {
		t.Fatalf("drained-attr row sender=%v want %v", drainedAttrs[0].SenderRunID, senderBRunID)
	}
	if drainedAttrs[0].TopicKind != "attribute" {
		t.Fatalf("drained-attr row topic=%q want attribute", drainedAttrs[0].TopicKind)
	}
}
