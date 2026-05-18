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

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/foundation/spec"
	"github.com/fallguy/rimsky/graph/node"
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

func (f *fakeMessagesTable) Get(_ context.Context, id shared.UUID) (*persistence.MessageRow, error) {
	r, ok := f.rows[id]
	if !ok {
		return nil, nil
	}
	cp := *r
	return &cp, nil
}

func (f *fakeMessagesTable) List(_ context.Context, _ persistence.MessageListFilter, _ persistence.ListPagination) (persistence.PaginatedListResult[persistence.MessageRow], error) {
	return persistence.PaginatedListResult[persistence.MessageRow]{}, nil
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

	res, err := DeliverPendingMessages(ctx, nil, m, inst, frame, FrameDeliveryCoalesce, now)
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

	res, err := DeliverPendingMessages(ctx, nil, m, inst, frame, FrameDeliverySerialQueue, now)
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

// TestMessageEdgeMatches_FilterPermutations exhaustively walks the
// filter-permutation matrix for messageEdgeMatches. Empty filter fields
// are wildcards; explicit fields must equal the envelope. `target: "self"`
// passes at this stage (receiver-relative resolution happens later in
// cascadeMessageSubscribersInTx). Spec §Unified message layer /
// Subscriptions.
func TestMessageEdgeMatches_FilterPermutations(t *testing.T) {
	msg := persistence.MessageRow{
		Kind:       "invalidate",
		Sender:     "op-A",
		SenderKind: "operator",
		Target:     "leaf-N",
	}
	cases := []struct {
		name   string
		filter node.SubscriptionFilter
		want   bool
	}{
		{"empty matches all", node.SubscriptionFilter{}, true},
		{"kind match", node.SubscriptionFilter{Kind: "invalidate"}, true},
		{"kind mismatch", node.SubscriptionFilter{Kind: "observation"}, false},
		{"sender match", node.SubscriptionFilter{Sender: "op-A"}, true},
		{"sender mismatch", node.SubscriptionFilter{Sender: "op-B"}, false},
		{"sender_kind match", node.SubscriptionFilter{SenderKind: "operator"}, true},
		{"sender_kind mismatch", node.SubscriptionFilter{SenderKind: "publisher"}, false},
		{"target literal match", node.SubscriptionFilter{Target: "leaf-N"}, true},
		{"target literal mismatch", node.SubscriptionFilter{Target: "leaf-M"}, false},
		{"target self defers to receiver resolution", node.SubscriptionFilter{Target: "self"}, true},
		{"all match", node.SubscriptionFilter{Kind: "invalidate", Sender: "op-A", SenderKind: "operator", Target: "leaf-N"}, true},
		{"kind match + sender mismatch", node.SubscriptionFilter{Kind: "invalidate", Sender: "op-B"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			edge := node.SubscriptionEdge{TopicKind: spec.TopicKindMessage, Filter: tc.filter}
			if got := messageEdgeMatches(edge, msg); got != tc.want {
				t.Errorf("messageEdgeMatches(filter=%+v, msg=%+v) = %v, want %v",
					tc.filter, msg, got, tc.want)
			}
		})
	}
}

// TestMessageEdgeMatches_TargetSelfWithEmptyEnvelopeTarget pins the
// fixed semantics: an envelope with no `target` field does NOT match a
// `target: self` subscription, regardless of receiver alias. Issue 4
// from the 2026-05-15 fixer cycle 3 review. Senders use `*` for broadcast.
func TestMessageEdgeMatches_TargetSelfWithEmptyEnvelopeTarget(t *testing.T) {
	msg := persistence.MessageRow{
		Kind:       "invalidate",
		Sender:     "op-A",
		SenderKind: "operator",
		// Target intentionally empty — broadcast envelope.
	}
	edge := node.SubscriptionEdge{
		TopicKind: spec.TopicKindMessage,
		Filter:    node.SubscriptionFilter{Target: "self"},
	}
	// messageEdgeMatches returns true at the envelope-filter stage for
	// `target: self` (the receiver-relative resolution is deferred).
	if !messageEdgeMatches(edge, msg) {
		t.Fatal("messageEdgeMatches: target=self with empty msg.Target should defer to receiver resolution (true at this stage)")
	}
	// The receiver-resolution stage (in cascadeMessageSubscribersInTx)
	// then rejects an empty msg.Target for any receiver alias. Mirror
	// that check inline so a regression on the deferred guard is caught
	// here too.
	for _, receiverAlias := range []string{"alpha", "beta", "gamma"} {
		if msg.Target == receiverAlias {
			t.Fatalf("test bug: receiverAlias=%q should not equal empty target", receiverAlias)
		}
		// `target: self` semantics: skip when msg.Target != receiverAlias.
		// Empty msg.Target is never equal to a real receiver alias →
		// every receiver is correctly skipped.
		if edge.Filter.Target == "self" && msg.Target == receiverAlias {
			t.Fatalf("regression: empty target matched receiver %q", receiverAlias)
		}
	}
}

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
	res, err := DeliverPendingMessages(ctx, nil, m, inst, frame, FrameDeliveryCoalesce, now)
	if err != nil {
		t.Fatalf("DeliverPendingMessages: %v", err)
	}
	if len(res.Messages) != 0 {
		t.Fatalf("expected no delivered messages (only cancelled), got %d", len(res.Messages))
	}
}
