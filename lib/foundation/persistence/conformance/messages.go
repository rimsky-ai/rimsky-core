// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: message

//	@concept: message
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
	t.Helper()
	defer d.Close()
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	frameA := fix.FrameID
	frameBMsgID := shared.UUID(uuid.New())
	var frameB shared.UUID
	if err := inTx(ctx, store, func(tx persistence.Tx) error {
		if err := store.Messages().Insert(ctx, tx, persistence.EnqueueMessageRequest{
			ID:         frameBMsgID,
			InstanceID: fix.InstanceID,
			Type:       "fixture/message",
			Sender:     "operator",
			SenderKind: "operator",
		}); err != nil {
			return err
		}
		fid, err := store.Frames().InsertFrame(ctx, fix.InstanceID, frameBMsgID, 600000, tx)
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
	_ = enqueueAndDeliver(t, shared.UUID{}, map[string]any{"partition_request_override": map[string]any{"c": 3}})

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
