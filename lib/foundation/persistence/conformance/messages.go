// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: message
package conformance

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func testMessagesListByFrameID(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	frameA := fix.FrameID
	frameBMsgID := shared.UUID(uuid.New())
	var frameB shared.UUID
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if _, err := store.Frames().MarkFrameEnded(ctx, frameA, tx); err != nil {
			return err
		}
		if err := store.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID:         frameBMsgID,
			InstanceID: fix.InstanceID,
			Type:       "fixture/message",
			Sender:     "operator",
			SenderKind: "operator",
		}); err != nil {
			return err
		}
		frameBScope := seedMainRunScopeForInstance(ctx, t, tx, store, fix.InstanceID)
		fid, err := store.Frames().InsertRunningFrame(ctx, fix.InstanceID, frameBMsgID, frameBScope, tx)
		if err != nil {
			return err
		}
		frameB = fid
		return nil
	}); err != nil {
		t.Fatalf("seed frameB: %v", err)
	}

	enqueueAndDeliver := func(t *testing.T, frame shared.UUID, payload map[string]any) shared.UUID {
		t.Helper()
		msgID := shared.UUID(uuid.New())
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			return store.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
				ID:         msgID,
				InstanceID: fix.InstanceID,
				Type:       "fixture/message",
				Sender:     "operator",
				SenderKind: "operator",
				Payload:    body,
				ReceivedAt: time.Now().UTC(),
			})
		}); err != nil {
			t.Fatalf("Messages.Insert: %v", err)
		}
		if frame != (shared.UUID{}) {
			if err := inTx(ctx, store, func(tx persistence.Tx) error {
				ok, err := store.Messages().MarkDelivered(ctx, tx, msgID, frame, time.Now().UTC())
				if err != nil {
					return err
				}
				if !ok {
					t.Fatalf("MarkDelivered: expected one row updated for %s", msgID)
				}
				return nil
			}); err != nil {
				t.Fatalf("Messages.MarkDelivered: %v", err)
			}
		}
		return msgID
	}

	msgA := enqueueAndDeliver(t, frameA, map[string]any{"partition_request_override": map[string]any{"a": 1}})
	msgB := enqueueAndDeliver(t, frameB, map[string]any{"partition_request_override": map[string]any{"b": 2}})
	msgC := enqueueAndDeliver(t, shared.UUID{}, map[string]any{"partition_request_override": map[string]any{"c": 3}})

	list := func(f persistence.MessageListFilter) []persistence.MessageRow {
		t.Helper()
		res, err := store.Messages().List(ctx, f, persistence.ListPagination{Limit: 50})
		if err != nil {
			t.Fatalf("Messages.List: %v", err)
		}
		return res.Rows
	}

	if got := list(persistence.MessageListFilter{InstanceID: &fix.InstanceID}); len(got) != 5 {
		t.Fatalf("no FrameID filter = %d rows, want 5", len(got))
	}

	gotA := list(persistence.MessageListFilter{FrameID: &frameA})
	if len(gotA) != 1 {
		t.Fatalf("FrameID(A) = %d rows, want 1", len(gotA))
	}
	if gotA[0].ID != msgA {
		t.Fatalf("FrameID(A) returned %s, want %s", gotA[0].ID, msgA)
	}
	if gotA[0].FrameID == nil || *gotA[0].FrameID != frameA {
		t.Fatalf("FrameID(A) row frame_id mismatch: %v", gotA[0].FrameID)
	}

	gotB := list(persistence.MessageListFilter{FrameID: &frameB})
	if len(gotB) != 1 {
		t.Fatalf("FrameID(B) = %d rows, want 1", len(gotB))
	}
	if gotB[0].ID != msgB {
		t.Fatalf("FrameID(B) returned %s, want %s", gotB[0].ID, msgB)
	}

	unknownFrame := shared.UUID(uuid.New())
	if got := list(persistence.MessageListFilter{FrameID: &unknownFrame}); len(got) != 0 {
		t.Fatalf("FrameID(unknown) = %d rows, want 0", len(got))
	}

	pending := true
	settled := false
	gotPending := list(persistence.MessageListFilter{InstanceID: &fix.InstanceID, Pending: &pending})
	gotSettled := list(persistence.MessageListFilter{InstanceID: &fix.InstanceID, Pending: &settled})
	if len(gotPending)+len(gotSettled) != 5 {
		t.Fatalf("Pending=true (%d) + Pending=false (%d) must partition all 5 rows",
			len(gotPending), len(gotSettled))
	}
	inRows := func(rows []persistence.MessageRow, id shared.UUID) bool {
		for _, r := range rows {
			if r.ID == id {
				return true
			}
		}
		return false
	}
	if !inRows(gotPending, msgC) {
		t.Fatalf("undelivered msgC must appear under Pending=true; pending rows = %v", gotPending)
	}
	if !inRows(gotSettled, msgA) || !inRows(gotSettled, msgB) {
		t.Fatalf("delivered msgA and msgB must appear under Pending=false; settled rows = %v", gotSettled)
	}
	if !inRows(gotPending, fix.MessageID) || !inRows(gotPending, frameBMsgID) {
		t.Fatalf("the two frame-triggering messages are never marked delivered and must appear under "+
			"Pending=true; pending rows = %v", gotPending)
	}
	if len(gotPending) != 3 {
		t.Fatalf("Pending=true = %d rows, want exactly 3 (msgC + the two frame-triggering messages)", len(gotPending))
	}
	if len(gotSettled) != 2 {
		t.Fatalf("Pending=false = %d rows, want exactly 2 (msgA + msgB)", len(gotSettled))
	}
	for _, r := range gotPending {
		if inRows(gotSettled, r.ID) {
			t.Fatalf("message %s appears in both Pending=true and Pending=false results", r.ID)
		}
	}

	var deliveredA []persistence.MessageRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.Messages().ListDeliveredForFrame(ctx, tx, frameA)
		deliveredA = r
		return err
	}); err != nil {
		t.Fatalf("ListDeliveredForFrame(A) inside tx: %v", err)
	}
	if len(deliveredA) != 1 || deliveredA[0].ID != msgA {
		t.Fatalf("ListDeliveredForFrame(A) = %v, want exactly msgA=%s", deliveredA, msgA)
	}
	var deliveredUnknown []persistence.MessageRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.Messages().ListDeliveredForFrame(ctx, tx, unknownFrame)
		deliveredUnknown = r
		return err
	}); err != nil {
		t.Fatalf("ListDeliveredForFrame(unknown) inside tx: %v", err)
	}
	if len(deliveredUnknown) != 0 {
		t.Fatalf("ListDeliveredForFrame(unknown) = %d rows, want 0", len(deliveredUnknown))
	}
}

// @concept: message
func testMessagesMarkDeliveredExcludesCancelled(t *testing.T, d persistence.Database) {
	t.Helper()
	defer d.Close()
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	msgID := shared.UUID(uuid.New())
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		return store.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID:         msgID,
			InstanceID: fix.InstanceID,
			Type:       "fixture/message",
			Sender:     "operator",
			SenderKind: "operator",
			ReceivedAt: time.Now().UTC(),
		})
	}); err != nil {
		t.Fatalf("Messages.Insert: %v", err)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		n, err := store.Messages().CancelPendingForInstance(ctx, tx, fix.InstanceID)
		if err != nil {
			return err
		}
		if n < 1 {
			t.Fatalf("CancelPendingForInstance: cancelled %d rows, want at least 1", n)
		}
		return nil
	}); err != nil {
		t.Fatalf("Messages.CancelPendingForInstance: %v", err)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		ok, err := store.Messages().MarkDelivered(ctx, tx, msgID, fix.FrameID, time.Now().UTC())
		if err != nil {
			return err
		}
		if ok {
			t.Fatalf("MarkDelivered must not affect a cancelled message (coalesce-cancelled messages must never be delivered)")
		}
		return nil
	}); err != nil {
		t.Fatalf("Messages.MarkDelivered: %v", err)
	}

	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		row, err := store.Messages().GetInTx(ctx, tx, msgID)
		if err != nil {
			return err
		}
		if row == nil {
			t.Fatalf("expected message row to still exist")
		}
		if row.DeliveredAt != nil {
			t.Fatalf("a cancelled message's delivered_at must remain nil after a rejected MarkDelivered, got %v", row.DeliveredAt)
		}
		if !row.Cancelled {
			t.Fatalf("message must still be marked cancelled")
		}
		return nil
	}); err != nil {
		t.Fatalf("Messages.GetInTx: %v", err)
	}
}

// @concept: observability
func testMessagesListBySender(t *testing.T, d persistence.Database) {
	t.Helper()
	defer d.Close()
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	insert := func(sender, senderKind string) shared.UUID {
		t.Helper()
		id := shared.UUID(uuid.New())
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			return store.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
				ID:         id,
				InstanceID: fix.InstanceID,
				Type:       "fixture/message",
				Sender:     sender,
				SenderKind: senderKind,
				ReceivedAt: time.Now().UTC(),
			})
		}); err != nil {
			t.Fatalf("Messages.Insert(sender=%s): %v", sender, err)
		}
		return id
	}

	msgA := insert("publisher-a", "publisher")
	msgB := insert("publisher-b", "publisher")
	insert("operator", "operator")

	list := func(f persistence.MessageListFilter) []persistence.MessageRow {
		t.Helper()
		res, err := store.Messages().List(ctx, f, persistence.ListPagination{Limit: 50})
		if err != nil {
			t.Fatalf("Messages.List: %v", err)
		}
		return res.Rows
	}

	gotA := list(persistence.MessageListFilter{InstanceID: &fix.InstanceID, Sender: "publisher-a"})
	if len(gotA) != 1 || gotA[0].ID != msgA {
		t.Fatalf("Sender(publisher-a) = %v, want exactly [%s]", gotA, msgA)
	}

	gotB := list(persistence.MessageListFilter{InstanceID: &fix.InstanceID, Sender: "publisher-b"})
	if len(gotB) != 1 || gotB[0].ID != msgB {
		t.Fatalf("Sender(publisher-b) = %v, want exactly [%s]", gotB, msgB)
	}

	gotUnknown := list(persistence.MessageListFilter{InstanceID: &fix.InstanceID, Sender: "publisher-nonexistent"})
	if len(gotUnknown) != 0 {
		t.Fatalf("Sender(publisher-nonexistent) = %d rows, want 0", len(gotUnknown))
	}

	gotKindOnly := list(persistence.MessageListFilter{InstanceID: &fix.InstanceID, SenderKind: "publisher"})
	if len(gotKindOnly) != 2 {
		t.Fatalf("SenderKind(publisher) = %d rows, want 2 (both publisher-a and publisher-b)", len(gotKindOnly))
	}
}

func testMessagesListPendingForInstanceReturnsAllPending(t *testing.T, d persistence.Database) {
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	msgIDs := make([]shared.UUID, 3)
	for i := range msgIDs {
		msgIDs[i] = shared.UUID(uuid.New())
		if err := inTx(ctx, store, func(tx persistence.Tx) error {
			return store.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
				ID:         msgIDs[i],
				InstanceID: fix.InstanceID,
				Type:       "fixture/message",
				Sender:     "operator",
				SenderKind: "operator",
				ReceivedAt: time.Now().UTC().Add(time.Duration(i) * time.Millisecond),
			})
		}); err != nil {
			t.Fatalf("Messages.Insert(%d): %v", i, err)
		}
	}

	var pending []persistence.MessageRow
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		r, err := store.Messages().ListPendingForInstance(ctx, tx, fix.InstanceID)
		pending = r
		return err
	}); err != nil {
		t.Fatalf("ListPendingForInstance: %v", err)
	}
	if len(pending) < len(msgIDs) {
		t.Fatalf("ListPendingForInstance returned %d rows, want at least %d (a List method must not silently truncate)",
			len(pending), len(msgIDs))
	}
	got := map[shared.UUID]bool{}
	for _, r := range pending {
		got[r.ID] = true
	}
	for _, id := range msgIDs {
		if !got[id] {
			t.Fatalf("ListPendingForInstance missing seeded message %s: got %v", id, pending)
		}
	}
}
