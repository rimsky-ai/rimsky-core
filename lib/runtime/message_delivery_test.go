// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @blessed-invariant: no-silent-override-coalesce — exercised here: coalesce delivery preserves the latest payload without silent override.

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
		ID:                  req.ID,
		InstanceID:          req.InstanceID,
		Kind:                req.Kind,
		Sender:              req.Sender,
		SenderKind:          req.SenderKind,
		Target:              req.Target,
		Payload:             req.Payload,
		BackfillOperationID: req.BackfillOperationID,
		ReceivedAt:          req.ReceivedAt,
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

func (f *fakeMessagesTable) MarkCancelled(_ context.Context, _ persistence.Tx, op shared.UUID, at time.Time) (int, error) {
	n := 0
	for _, r := range f.rows {
		if r.BackfillOperationID != nil && *r.BackfillOperationID == op && r.DeliveredAt == nil {
			r.Cancelled = true
			t := at
			r.DeliveredAt = &t
			n++
		}
	}
	return n, nil
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
		if filter.BackfillOperationID != nil {
			if r.BackfillOperationID == nil || *r.BackfillOperationID != *filter.BackfillOperationID {
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
		Kind: "invalidate", Sender: "op-A", SenderKind: "operator",
	}
	if err := EnqueueMessage(ctx, nil, m, good); err != nil {
		t.Fatalf("EnqueueMessage(good): %v", err)
	}

	// Empty ID rejected.
	if err := EnqueueMessage(ctx, nil, m, persistence.EnqueueMessageRequest{}); err == nil {
		t.Fatal("EnqueueMessage(empty): expected error")
	}

	// Unknown sender_kind rejected.
	bad := good
	bad.ID = shared.UUID(uuid.New())
	bad.SenderKind = "bogus"
	if err := EnqueueMessage(ctx, nil, m, bad); err == nil {
		t.Fatal("EnqueueMessage(bogus sender_kind): expected error")
	}
}

// TestDeliverPendingMessages_Coalesce verifies coalesce mode delivers
// every pending message into the frame.
func TestDeliverPendingMessages_Coalesce(t *testing.T) {
	m := newFakeMessages()
	ctx := context.Background()
	inst := shared.UUID(uuid.New())
	frame := shared.UUID(uuid.New())
	now := time.Now().UTC()

	for i := 0; i < 3; i++ {
		_ = m.Insert(ctx, nil, persistence.EnqueueMessageRequest{
			ID: shared.UUID(uuid.New()), InstanceID: inst, Kind: "invalidate",
			Sender: "op-A", SenderKind: "operator",
			ReceivedAt: now.Add(time.Duration(i) * time.Second),
		})
	}

	res, err := DeliverPendingMessages(ctx, nil, m, inst, frame, FrameDeliveryCoalesce, now, nil)
	if err != nil {
		t.Fatalf("DeliverPendingMessages: %v", err)
	}
	if len(res.Messages) != 3 {
		t.Fatalf("expected 3 delivered messages, got %d", len(res.Messages))
	}
	for _, msg := range res.Messages {
		if msg.DeliveredAt == nil || msg.FrameID == nil || *msg.FrameID != frame {
			t.Fatalf("message %s missing delivery fields: %+v", msg.ID, msg)
		}
	}
}

// TestDeliverPendingMessages_SerialQueue picks only the oldest.
func TestDeliverPendingMessages_SerialQueue(t *testing.T) {
	m := newFakeMessages()
	ctx := context.Background()
	inst := shared.UUID(uuid.New())
	frame := shared.UUID(uuid.New())
	now := time.Now().UTC()

	first := shared.UUID(uuid.New())
	second := shared.UUID(uuid.New())
	_ = m.Insert(ctx, nil, persistence.EnqueueMessageRequest{
		ID: first, InstanceID: inst, Kind: "invalidate",
		Sender: "op-A", SenderKind: "operator",
		ReceivedAt: now,
	})
	_ = m.Insert(ctx, nil, persistence.EnqueueMessageRequest{
		ID: second, InstanceID: inst, Kind: "invalidate",
		Sender: "op-A", SenderKind: "operator",
		ReceivedAt: now.Add(time.Second),
	})

	res, err := DeliverPendingMessages(ctx, nil, m, inst, frame, FrameDeliverySerialQueue, now, nil)
	if err != nil {
		t.Fatalf("DeliverPendingMessages: %v", err)
	}
	if len(res.Messages) != 1 || res.Messages[0].ID != first {
		t.Fatalf("expected only %s delivered, got %+v", first, res.Messages)
	}
	// Second remains pending.
	if pending, _ := m.ListPendingForInstance(ctx, nil, inst); len(pending) != 1 || pending[0].ID != second {
		t.Fatalf("expected second message pending, got %+v", pending)
	}
}

// Under the 2026-05-23 signal-taxonomy reshape the per-envelope
// structured filter (Kind / Sender / SenderKind / Target) retired in
// favor of CEL when: predicates on the emitted
// `message/<kind>/<sender_kind>/<target>` signal. The legacy
// `messageEdgeMatches` helper retired; matching now happens inside
// cascadeMessageSubscribersInTx via SubscriptionEdgeMap.Match + CEL
// evaluation. Scenario coverage lives in
// test/scenarios/messages/message_cascade_e2e_test.go.

// TestDeliverPendingMessages_SkipsCancelled — pre-cancelled rows are
// already delivered_at-stamped and never re-delivered.
func TestDeliverPendingMessages_SkipsCancelled(t *testing.T) {
	m := newFakeMessages()
	ctx := context.Background()
	inst := shared.UUID(uuid.New())
	frame := shared.UUID(uuid.New())
	now := time.Now().UTC()
	op := shared.UUID(uuid.New())

	_ = m.Insert(ctx, nil, persistence.EnqueueMessageRequest{
		ID: shared.UUID(uuid.New()), InstanceID: inst, Kind: "invalidate",
		Sender: "op-A", SenderKind: "operator", BackfillOperationID: &op,
		ReceivedAt: now,
	})
	if _, err := m.MarkCancelled(ctx, nil, op, now); err != nil {
		t.Fatalf("MarkCancelled: %v", err)
	}
	res, err := DeliverPendingMessages(ctx, nil, m, inst, frame, FrameDeliveryCoalesce, now, nil)
	if err != nil {
		t.Fatalf("DeliverPendingMessages: %v", err)
	}
	if len(res.Messages) != 0 {
		t.Fatalf("expected no delivered messages (only cancelled), got %d", len(res.Messages))
	}
}

// sharedReceiverResolver maps every message to one receiver node type, so
// the coalesce conflict decision reduces to payload equality — exactly
// the no-silent-override-loss property the conflict-aware mode protects.
func sharedReceiverResolver(persistence.MessageRow) []string { return []string{"fan_out"} }

// TestDeliverPendingMessages_CoalesceSameValueCoalesces — under coalesce,
// two messages that bind the same payload-reading node to the SAME value
// coalesce into one frame (idempotent bindings are not a conflict).
func TestDeliverPendingMessages_CoalesceSameValueCoalesces(t *testing.T) {
	m := newFakeMessages()
	ctx := context.Background()
	inst := shared.UUID(uuid.New())
	frame := shared.UUID(uuid.New())
	now := time.Now().UTC()

	same := []byte(`{"partition_request_override":{"key":"A"}}`)
	for i := 0; i < 2; i++ {
		_ = m.Insert(ctx, nil, persistence.EnqueueMessageRequest{
			ID: shared.UUID(uuid.New()), InstanceID: inst, Kind: "invalidate",
			Sender: "op-A", SenderKind: "operator", Payload: same,
			ReceivedAt: now.Add(time.Duration(i) * time.Second),
		})
	}
	res, err := DeliverPendingMessages(ctx, nil, m, inst, frame, FrameDeliveryCoalesce, now, sharedReceiverResolver)
	if err != nil {
		t.Fatalf("DeliverPendingMessages: %v", err)
	}
	if len(res.Messages) != 2 {
		t.Fatalf("same-value coalesce: expected both delivered into one frame, got %d", len(res.Messages))
	}
	if pending, _ := m.ListPendingForInstance(ctx, nil, inst); len(pending) != 0 {
		t.Fatalf("same-value coalesce: expected 0 pending, got %d", len(pending))
	}
}

// TestDeliverPendingMessages_CoalesceDifferentValuesSplit — under coalesce,
// two backfills with DIFFERENT overrides targeting the same payload-reading
// node split into consecutive frames (received-order), neither lost. The
// regression guard for silent override loss.
func TestDeliverPendingMessages_CoalesceDifferentValuesSplit(t *testing.T) {
	m := newFakeMessages()
	ctx := context.Background()
	inst := shared.UUID(uuid.New())
	frame1 := shared.UUID(uuid.New())
	frame2 := shared.UUID(uuid.New())
	now := time.Now().UTC()

	overrideA := []byte(`{"partition_request_override":{"key":"A"}}`)
	overrideB := []byte(`{"partition_request_override":{"key":"B"}}`)
	first := shared.UUID(uuid.New())
	second := shared.UUID(uuid.New())
	_ = m.Insert(ctx, nil, persistence.EnqueueMessageRequest{
		ID: first, InstanceID: inst, Kind: "invalidate",
		Sender: "op-A", SenderKind: "operator", Payload: overrideA,
		ReceivedAt: now,
	})
	_ = m.Insert(ctx, nil, persistence.EnqueueMessageRequest{
		ID: second, InstanceID: inst, Kind: "invalidate",
		Sender: "op-A", SenderKind: "operator", Payload: overrideB,
		ReceivedAt: now.Add(time.Second),
	})

	// Frame 1: only the first (older, override A); B conflicts and stays pending.
	res1, err := DeliverPendingMessages(ctx, nil, m, inst, frame1, FrameDeliveryCoalesce, now, sharedReceiverResolver)
	if err != nil {
		t.Fatalf("DeliverPendingMessages frame1: %v", err)
	}
	if len(res1.Messages) != 1 || res1.Messages[0].ID != first {
		t.Fatalf("frame1: expected only the first (override A), got %+v", res1.Messages)
	}
	pending, _ := m.ListPendingForInstance(ctx, nil, inst)
	if len(pending) != 1 || pending[0].ID != second {
		t.Fatalf("frame1: expected the second (override B) still pending, got %+v", pending)
	}

	// Frame 2: the second (override B) delivers next, in order. Nothing lost.
	res2, err := DeliverPendingMessages(ctx, nil, m, inst, frame2, FrameDeliveryCoalesce, now, sharedReceiverResolver)
	if err != nil {
		t.Fatalf("DeliverPendingMessages frame2: %v", err)
	}
	if len(res2.Messages) != 1 || res2.Messages[0].ID != second {
		t.Fatalf("frame2: expected the second (override B), got %+v", res2.Messages)
	}
	if pending, _ := m.ListPendingForInstance(ctx, nil, inst); len(pending) != 0 {
		t.Fatalf("frame2: expected 0 pending after both delivered, got %d", len(pending))
	}
}

// TestDeliverPendingMessages_CoalesceDistinctNodesCoalesce — two messages
// that bind DIFFERENT payload-reading nodes do not conflict (no shared
// receiver), so they coalesce into one frame even with distinct payloads.
func TestDeliverPendingMessages_CoalesceDistinctNodesCoalesce(t *testing.T) {
	m := newFakeMessages()
	ctx := context.Background()
	inst := shared.UUID(uuid.New())
	frame := shared.UUID(uuid.New())
	now := time.Now().UTC()

	// Resolver routes each message to a distinct receiver keyed off Target.
	resolve := func(msg persistence.MessageRow) []string { return []string{"recv_" + msg.Target} }

	_ = m.Insert(ctx, nil, persistence.EnqueueMessageRequest{
		ID: shared.UUID(uuid.New()), InstanceID: inst, Kind: "invalidate",
		Sender: "op-A", SenderKind: "operator", Target: "alpha",
		Payload: []byte(`{"x":1}`), ReceivedAt: now,
	})
	_ = m.Insert(ctx, nil, persistence.EnqueueMessageRequest{
		ID: shared.UUID(uuid.New()), InstanceID: inst, Kind: "invalidate",
		Sender: "op-A", SenderKind: "operator", Target: "beta",
		Payload: []byte(`{"x":2}`), ReceivedAt: now.Add(time.Second),
	})
	res, err := DeliverPendingMessages(ctx, nil, m, inst, frame, FrameDeliveryCoalesce, now, resolve)
	if err != nil {
		t.Fatalf("DeliverPendingMessages: %v", err)
	}
	if len(res.Messages) != 2 {
		t.Fatalf("distinct-node coalesce: expected both delivered into one frame, got %d", len(res.Messages))
	}
}
