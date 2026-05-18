// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// wait_set.go — WaitSetTable conformance fixture. Exercises the four
// methods against both drivers (postgres + sqlite). See the
// subscription-cascade spec at
// .ok-planner/specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md.
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

	// DeleteBySender(senderA) removes the state-direct row only.
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.WaitSet().DeleteBySender(ctx, fix.FrameID, senderARunID, tx)
	}); err != nil {
		t.Fatalf("DeleteBySender: %v", err)
	}
	var remaining []persistence.WaitSetRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		rows, err := store.WaitSet().ListForReceiver(ctx, fix.FrameID, receiverRunID, tx)
		remaining = rows
		return err
	}); err != nil {
		t.Fatalf("ListForReceiver after delete: %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("after DeleteBySender(senderA): got %d rows want 1", len(remaining))
	}
	if remaining[0].SenderRunID != senderBRunID {
		t.Fatalf("remaining row sender=%v want %v", remaining[0].SenderRunID, senderBRunID)
	}
	if remaining[0].TopicKind != "attribute" {
		t.Fatalf("remaining row topic_kind=%q want %q", remaining[0].TopicKind, "attribute")
	}

	// DeleteBySender(senderB) drains the remainder.
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.WaitSet().DeleteBySender(ctx, fix.FrameID, senderBRunID, tx)
	}); err != nil {
		t.Fatalf("DeleteBySender senderB: %v", err)
	}
	var emptied []persistence.WaitSetRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		rows, err := store.WaitSet().ListForFrame(ctx, fix.FrameID, tx)
		emptied = rows
		return err
	}); err != nil {
		t.Fatalf("ListForFrame after drain: %v", err)
	}
	if len(emptied) != 0 {
		t.Fatalf("after final DeleteBySender: got %d rows want 0", len(emptied))
	}
}
