// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// fakeMessagesTable is an in-memory persistence.MessagesTable for unit
// tests of the delivery helpers. Mirrors the SQL shape but skips the
// real-driver round-trip.
type fakeMessagesTable struct {
	rows map[shared.UUID]*persistence.MessageRow
}

func newFakeMessages() *fakeMessagesTable {
	return &fakeMessagesTable{rows: make(map[shared.UUID]*persistence.MessageRow)}
}

func (f *fakeMessagesTable) Insert(_ context.Context, _ persistence.Tx, req persistence.EnqueueMessageRequest) error {
	f.rows[req.ID] = &persistence.MessageRow{
		ID:         req.ID,
		InstanceID: req.InstanceID,
		Type:       req.Type,
		Sender:     req.Sender,
		SenderKind: req.SenderKind,
		Payload:    req.Payload,
		ReceivedAt: req.ReceivedAt,
	}
	return nil
}

func (f *fakeMessagesTable) MarkDelivered(_ context.Context, _ persistence.Tx, id shared.UUID, frame shared.UUID, deliveredAt time.Time) (bool, error) {
	row, ok := f.rows[id]
	if !ok || row.DeliveredAt != nil {
		return false, nil
	}
	row.DeliveredAt = &deliveredAt
	fid := frame
	row.FrameID = &fid
	return true, nil
}

func (f *fakeMessagesTable) ListPendingForInstance(_ context.Context, _ persistence.Tx, instanceID shared.UUID) ([]persistence.MessageRow, error) {
	var out []persistence.MessageRow
	for _, r := range f.rows {
		if r.InstanceID != instanceID || r.DeliveredAt != nil {
			continue
		}
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ReceivedAt.Before(out[j].ReceivedAt) })
	return out, nil
}

func (f *fakeMessagesTable) ListDeliveredForFrame(_ context.Context, _ persistence.Tx, frame shared.UUID) ([]persistence.MessageRow, error) {
	var out []persistence.MessageRow
	for _, r := range f.rows {
		if r.FrameID == nil || *r.FrameID != frame {
			continue
		}
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ReceivedAt.Before(out[j].ReceivedAt) })
	return out, nil
}

func (f *fakeMessagesTable) Get(_ context.Context, id shared.UUID) (*persistence.MessageRow, error) {
	r, ok := f.rows[id]
	if !ok {
		return nil, nil
	}
	cp := *r
	return &cp, nil
}

func (f *fakeMessagesTable) GetInTx(ctx context.Context, _ persistence.Tx, id shared.UUID) (*persistence.MessageRow, error) {
	return f.Get(ctx, id)
}

func (f *fakeMessagesTable) List(_ context.Context, filter persistence.MessageListFilter, pag persistence.ListPagination) (persistence.PaginatedListResult[persistence.MessageRow], error) {
	var out []persistence.MessageRow
	for _, r := range f.rows {
		if filter.InstanceID != nil && r.InstanceID != *filter.InstanceID {
			continue
		}
		if filter.FrameID != nil {
			if r.FrameID == nil || *r.FrameID != *filter.FrameID {
				continue
			}
		}
		out = append(out, *r)
		if pag.Limit > 0 && len(out) >= pag.Limit {
			break
		}
	}
	return persistence.PaginatedListResult[persistence.MessageRow]{Rows: out}, nil
}

// TestEnqueueMessage_ValidatesShape asserts the helper rejects missing
// fields and unknown sender_kind values.
func TestEnqueueMessage_ValidatesShape(t *testing.T) {
	m := newFakeMessages()
	ctx := context.Background()
	good := persistence.EnqueueMessageRequest{
		ID: shared.UUID(uuid.New()), InstanceID: shared.UUID(uuid.New()),
		Type: "invalidate", Sender: "op-A", SenderKind: "operator",
	}
	if err := EnqueueMessage(ctx, nil, m, good); err != nil {
		t.Fatalf("EnqueueMessage(good): %v", err)
	}

	// @deliberate: Empty ID rejected.
	if err := EnqueueMessage(ctx, nil, m, persistence.EnqueueMessageRequest{}); err == nil {
		t.Fatal("EnqueueMessage(empty): expected error")
	}

	// @deliberate: Unknown sender_kind rejected.
	bad := good
	bad.ID = shared.UUID(uuid.New())
	bad.SenderKind = "bogus"
	if err := EnqueueMessage(ctx, nil, m, bad); err == nil {
		t.Fatal("EnqueueMessage(bogus sender_kind): expected error")
	}
}

// TestDeliverPendingMessages_OneMessagePerCall is the load-bearing
// regression guard for the one-message-per-frame invariant. Seed ≥10
// pending messages; assert DeliverPendingMessages returns exactly one
// per invocation, in received-order, and the rest stay pending.
//
// The cheaper shape "deliver everything pending and let downstream sort it
// out" silently collapses distinct override envelopes into one rerun and
// is the falsifier this test exists to catch.
func TestDeliverPendingMessages_OneMessagePerCall(t *testing.T) {
	m := newFakeMessages()
	ctx := context.Background()
	inst := shared.UUID(uuid.New())
	frame := shared.UUID(uuid.New())
	now := time.Now().UTC()

	const total = 10
	ids := make([]shared.UUID, total)
	for i := 0; i < total; i++ {
		ids[i] = shared.UUID(uuid.New())
		_ = m.Insert(ctx, nil, persistence.EnqueueMessageRequest{
			ID: ids[i], InstanceID: inst, Type: "invalidate",
			Sender: "op-A", SenderKind: "operator",
			ReceivedAt: now.Add(time.Duration(i) * time.Second),
		})
	}

	for i := 0; i < total; i++ {
		res, err := DeliverPendingMessages(ctx, nil, m, inst, frame, now)
		if err != nil {
			t.Fatalf("DeliverPendingMessages[%d]: %v", i, err)
		}
		if len(res.Messages) != 1 {
			t.Fatalf("DeliverPendingMessages[%d] returned %d messages, want 1 (one-message-per-frame)",
				i, len(res.Messages))
		}
		if res.Messages[0].ID != ids[i] {
			t.Fatalf("DeliverPendingMessages[%d] returned id=%s, want oldest pending %s",
				i, res.Messages[0].ID, ids[i])
		}
		remaining, _ := m.ListPendingForInstance(ctx, nil, inst)
		if want := total - (i + 1); len(remaining) != want {
			t.Fatalf("after delivery %d: pending=%d, want %d", i, len(remaining), want)
		}
	}

	// @deliberate: One more call yields zero — the queue is drained.
	res, err := DeliverPendingMessages(ctx, nil, m, inst, frame, now)
	if err != nil {
		t.Fatalf("DeliverPendingMessages(drained): %v", err)
	}
	if len(res.Messages) != 0 {
		t.Fatalf("expected empty deliver-set after drain, got %d", len(res.Messages))
	}
}

// TestDeliverPendingMessages_DeliveredMatchesTrigger pins the invariant
// behind the reworded blessed-invariant comment on
// cascadeMessageVirtualNodeSettleInTx: the message
// `Messages().ListDeliveredForFrame(frameID)` returns is the same row
// the frame's `triggering_message_id` column points at. The two queries
// hit different SQL paths and the equality is by construction; this
// test pins it so a refactor that splits the two row identities (e.g. a
// future delivery sweep that stamps `frame_id` on a different envelope
// than the frame's triggering message) fails loudly. The fake exercises
// the contract: one pending message + a frame the caller pairs with it
// → after delivery the listed row IS that message id, NOT some other.
func TestDeliverPendingMessages_DeliveredMatchesTrigger(t *testing.T) {
	m := newFakeMessages()
	ctx := context.Background()
	inst := shared.UUID(uuid.New())
	frame := shared.UUID(uuid.New())
	now := time.Now().UTC()

	msgID := shared.UUID(uuid.New())
	_ = m.Insert(ctx, nil, persistence.EnqueueMessageRequest{
		ID: msgID, InstanceID: inst, Type: "ping/recheck",
		Sender: "op-A", SenderKind: "operator", ReceivedAt: now,
	})

	// @deliberate: Deliver stamps delivered_at + frame_id = `frame` on the row.
	res, err := DeliverPendingMessages(ctx, nil, m, inst, frame, now)
	if err != nil {
		t.Fatalf("DeliverPendingMessages: %v", err)
	}
	if len(res.Messages) != 1 || res.Messages[0].ID != msgID {
		t.Fatalf("DeliverPendingMessages: got %+v, want one row with id=%s", res.Messages, msgID)
	}

	// @deliberate: ListDeliveredForFrame must return the exact same id — substitution reads through this path; the frame's triggering_message_id (in real deployments) points at the same row.
	delivered, err := m.ListDeliveredForFrame(ctx, nil, frame)
	if err != nil {
		t.Fatalf("ListDeliveredForFrame: %v", err)
	}
	if len(delivered) != 1 {
		t.Fatalf("ListDeliveredForFrame: got %d rows, want exactly 1", len(delivered))
	}
	if delivered[0].ID != msgID {
		t.Fatalf("ListDeliveredForFrame row id = %s; want the delivered message id %s — substitution and frame-origin audit must read the same envelope",
			delivered[0].ID, msgID)
	}
}
