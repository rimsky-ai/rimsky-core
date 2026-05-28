// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N4 scenarios — frame_delivery_mode_serial_queue and
// frame_delivery_mode_coalesce.
//
// `serial_queue` delivers only the oldest pending message into the
// new frame; remaining messages stay pending until the next frame.
// `coalesce` (default) delivers all pending messages.
package messages

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

func TestFrameDeliveryMode_SerialQueueOnlyOldest(t *testing.T) {
	t.Parallel()
	m := newFakeMessages()
	ctx := context.Background()
	instanceID := shared.UUID(uuid.New())
	frameID := shared.UUID(uuid.New())
	t0 := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)

	for i := 0; i < 3; i++ {
		if err := runtime.EnqueueMessage(ctx, nil, m, persistence.EnqueueMessageRequest{
			ID:         shared.UUID(uuid.New()),
			InstanceID: instanceID,
			Kind:       "invalidate",
			Sender:     "operator/main",
			SenderKind: "operator",
			ReceivedAt: t0.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("EnqueueMessage[%d]: %v", i, err)
		}
	}
	delivered, err := runtime.DeliverPendingMessages(ctx, nil, m, instanceID, frameID, runtime.FrameDeliverySerialQueue, t0)
	if err != nil {
		t.Fatalf("DeliverPendingMessages: %v", err)
	}
	if len(delivered.Messages) != 1 {
		t.Fatalf("serial_queue: expected 1 delivered, got %d", len(delivered.Messages))
	}
	if !delivered.Messages[0].ReceivedAt.Equal(t0) {
		t.Errorf("serial_queue delivered the oldest? received_at: %v want %v",
			delivered.Messages[0].ReceivedAt, t0)
	}
	// Pin: remaining messages still pending.
	pending, err := m.ListPendingForInstance(ctx, nil, instanceID)
	if err != nil {
		t.Fatalf("ListPendingForInstance: %v", err)
	}
	if len(pending) != 2 {
		t.Errorf("serial_queue: expected 2 still pending, got %d", len(pending))
	}
}

func TestFrameDeliveryMode_CoalesceDeliversAll(t *testing.T) {
	t.Parallel()
	m := newFakeMessages()
	ctx := context.Background()
	instanceID := shared.UUID(uuid.New())
	frameID := shared.UUID(uuid.New())
	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		if err := runtime.EnqueueMessage(ctx, nil, m, persistence.EnqueueMessageRequest{
			ID:         shared.UUID(uuid.New()),
			InstanceID: instanceID,
			Kind:       "invalidate",
			Sender:     "operator/main",
			SenderKind: "operator",
			ReceivedAt: now.Add(time.Duration(i) * time.Millisecond),
		}); err != nil {
			t.Fatalf("EnqueueMessage[%d]: %v", i, err)
		}
	}
	delivered, err := runtime.DeliverPendingMessages(ctx, nil, m, instanceID, frameID, runtime.FrameDeliveryCoalesce, now)
	if err != nil {
		t.Fatalf("DeliverPendingMessages: %v", err)
	}
	if len(delivered.Messages) != 5 {
		t.Fatalf("coalesce: expected 5 delivered, got %d", len(delivered.Messages))
	}
	pending, err := m.ListPendingForInstance(ctx, nil, instanceID)
	if err != nil {
		t.Fatalf("ListPendingForInstance: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("coalesce: expected 0 pending after delivery, got %d", len(pending))
	}
}
