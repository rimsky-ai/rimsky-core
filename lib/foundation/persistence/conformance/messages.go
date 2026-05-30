// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// messages.go — MessagesTable conformance fixture. Exercises the
// MessageListFilter.FrameID predicate against both drivers (postgres +
// sqlite) so the per-driver `frame_id = ?` clause is honored by the real
// engine, not just the application layer.
//
// The FrameID filter backs fan-out acquisition's recovery of a frame's
// trigger message: at acquisition time the runtime fetches the delivered
// message bound to the frame so the node's partition_request can be
// substituted from the override the message carries (see
// runtime/runner_acquire_helpers.go::acquireFanOutIfDeclared).
//
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

// testMessagesListByFrameID seeds three messages on one instance —
// two delivered into two distinct frames, one left pending (no frame) —
// and pins that MessageListFilter.FrameID returns exactly the message(s)
// delivered into the named frame and never a row from a different frame
// nor the still-pending row. A nil FrameID is a no-op (returns all).
func testMessagesListByFrameID(t *testing.T, d persistence.Database) {
	t.Helper()
	defer d.Close()
	ctx := context.Background()
	fix := seedFixtureSet(ctx, t, d)
	store := d.Tables()

	// frameA is the fixture's running frame; frameB is a second distinct
	// frame id so a FrameID filter has a genuine cross-frame negative to
	// exclude. rimsky_messages.frame_id carries no FK and an instance
	// holds at most one live frame row (uq on rimsky_frames.instance_id,
	// the serial-queue model), so frameB is a synthetic id rather than a
	// second concurrent frame row — the filter under test reads
	// rimsky_messages.frame_id, not rimsky_frames.
	frameA := fix.FrameID
	frameB := shared.UUID(uuid.New())

	// enqueueAndDeliver inserts one message and, when frame is non-zero,
	// marks it delivered into that frame. Returns the message id.
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
				Kind:       "invalidate",
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
	// A still-pending message (no frame) must never match a FrameID filter.
	_ = enqueueAndDeliver(t, shared.UUID{}, map[string]any{"partition_request_override": map[string]any{"c": 3}})

	list := func(f persistence.MessageListFilter) []persistence.MessageRow {
		t.Helper()
		res, err := store.Messages().List(ctx, f, persistence.ListPagination{Limit: 50})
		if err != nil {
			t.Fatalf("Messages.List: %v", err)
		}
		return res.Rows
	}

	// nil FrameID is a no-op: all three messages for the instance.
	if got := list(persistence.MessageListFilter{InstanceID: &fix.InstanceID}); len(got) != 3 {
		t.Fatalf("no FrameID filter = %d rows, want 3", len(got))
	}

	// FrameID = frameA returns exactly msgA.
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

	// FrameID = frameB returns exactly msgB (proves cross-frame exclusion).
	gotB := list(persistence.MessageListFilter{FrameID: &frameB})
	if len(gotB) != 1 {
		t.Fatalf("FrameID(B) = %d rows, want 1", len(gotB))
	}
	if gotB[0].ID != msgB {
		t.Fatalf("FrameID(B) returned %s, want %s", gotB[0].ID, msgB)
	}

	// FrameID for a frame with no delivered message returns nothing —
	// guards against a predicate that always matches.
	unknownFrame := shared.UUID(uuid.New())
	if got := list(persistence.MessageListFilter{FrameID: &unknownFrame}); len(got) != 0 {
		t.Fatalf("FrameID(unknown) = %d rows, want 0", len(got))
	}

	// ListDeliveredForFrame is the tx-aware sibling fan-out acquisition
	// calls from inside the open acquisition tx (the tx-less List would
	// deadlock the single-conn SQLite driver there). It must return the
	// same single message for frameA, run cleanly inside a tx, and never
	// pick up the still-pending row.
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
