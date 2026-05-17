// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N4 scenario — multi_receiver_match.
//
// A single message can match multiple subscribers (target = "*" or
// glob). The delivery layer doesn't itself resolve subscribers — it
// just stamps the message and returns. The subscription walker
// runs against the delivered set. The N4 contract pinned here is
// that the delivery layer returns each delivered message with its
// FrameID stamped, so a downstream walker can iterate.
package messages

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/runtime"
)

func TestMultiReceiverMatch_AllDeliveredInCoalesceMode(t *testing.T) {
	t.Parallel()
	m := newFakeMessages()
	ctx := context.Background()
	instanceID := shared.UUID(uuid.New())
	frameID := shared.UUID(uuid.New())
	now := time.Now().UTC()
	const N = 4
	for i := 0; i < N; i++ {
		if err := runtime.EnqueueMessage(ctx, nil, m, persistence.EnqueueMessageRequest{
			ID:         shared.UUID(uuid.New()),
			InstanceID: instanceID,
			Kind:       "invalidate",
			Sender:     "operator/scheduler",
			SenderKind: "operator",
			Target:     "*",
			ReceivedAt: now.Add(time.Duration(i) * time.Millisecond),
		}); err != nil {
			t.Fatalf("EnqueueMessage[%d]: %v", i, err)
		}
	}
	delivered, err := runtime.DeliverPendingMessages(ctx, nil, m, instanceID, frameID, runtime.FrameDeliveryCoalesce, now)
	if err != nil {
		t.Fatalf("DeliverPendingMessages: %v", err)
	}
	if len(delivered.Messages) != N {
		t.Errorf("coalesce mode: expected %d delivered, got %d", N, len(delivered.Messages))
	}
	for i, msg := range delivered.Messages {
		if msg.FrameID == nil || *msg.FrameID != frameID {
			t.Errorf("delivered[%d].frame_id mismatch", i)
		}
	}
}
