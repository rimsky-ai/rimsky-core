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

	if err := EnqueueMessage(ctx, nil, m, persistence.EnqueueMessageRequest{}); err == nil {
		t.Fatal("EnqueueMessage(empty): expected error")
	}

	bad := good
	bad.ID = shared.UUID(uuid.New())
	bad.SenderKind = "bogus"
	if err := EnqueueMessage(ctx, nil, m, bad); err == nil {
		t.Fatal("EnqueueMessage(bogus sender_kind): expected error")
	}
}

func TestDeliverPendingMessages_OnlyDeliversFrameTrigger(t *testing.T) {
	m := newFakeMessages()
	ctx := context.Background()
	inst := shared.UUID(uuid.New())
	frame1 := shared.UUID(uuid.New())
	now := time.Now().UTC()

	triggerID := shared.UUID(uuid.New())
	siblingID := shared.UUID(uuid.New())
	_ = m.Insert(ctx, nil, persistence.EnqueueMessageRequest{
		ID: triggerID, InstanceID: inst, Type: "invalidate",
		Sender: "op-A", SenderKind: "operator", ReceivedAt: now,
	})
	_ = m.Insert(ctx, nil, persistence.EnqueueMessageRequest{
		ID: siblingID, InstanceID: inst, Type: "invalidate",
		Sender: "op-A", SenderKind: "operator",
		ReceivedAt: now.Add(time.Second),
	})

	res, err := DeliverPendingMessages(ctx, nil, m, inst, frame1, triggerID, now)
	if err != nil {
		t.Fatalf("DeliverPendingMessages: %v", err)
	}
	if len(res.Messages) != 1 || res.Messages[0].ID != triggerID {
		t.Fatalf("DeliverPendingMessages: got %+v, want exactly the trigger %s", res.Messages, triggerID)
	}

	remaining, _ := m.ListPendingForInstance(ctx, nil, inst)
	if len(remaining) != 1 || remaining[0].ID != siblingID {
		t.Fatalf("after delivering trigger, pending=%v, want one row with sibling %s — a message must only flow into its own frame, never into a sibling frame's delivery",
			remaining, siblingID)
	}

	res, err = DeliverPendingMessages(ctx, nil, m, inst, frame1, triggerID, now)
	if err != nil {
		t.Fatalf("DeliverPendingMessages (repeat): %v", err)
	}
	if len(res.Messages) != 0 {
		t.Fatalf("expected empty deliver-set on repeat call (idempotent), got %d", len(res.Messages))
	}
}

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

	res, err := DeliverPendingMessages(ctx, nil, m, inst, frame, msgID, now)
	if err != nil {
		t.Fatalf("DeliverPendingMessages: %v", err)
	}
	if len(res.Messages) != 1 || res.Messages[0].ID != msgID {
		t.Fatalf("DeliverPendingMessages: got %+v, want one row with id=%s", res.Messages, msgID)
	}

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
