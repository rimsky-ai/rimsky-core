// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

func TestDeadLetter_CancelledNotDelivered(t *testing.T) {
	t.Parallel()
	m := newFakeMessages()
	ctx := context.Background()
	instanceID := shared.UUID(uuid.New())
	frameID := shared.UUID(uuid.New())
	now := time.Now().UTC()

	triggerID := shared.UUID(uuid.New())
	if err := runtime.EnqueueMessage(ctx, nil, m, persistence.EnqueueMessageRequest{
		ID:         triggerID,
		InstanceID: instanceID,
		Type:       "invalidate",
		Sender:     "sensor-cron",
		SenderKind: "publisher",
		ReceivedAt: now,
	}); err != nil {
		t.Fatalf("EnqueueMessage live: %v", err)
	}
	delivered, err := runtime.DeliverPendingMessages(ctx, nil, m, instanceID, frameID, triggerID, now)
	if err != nil {
		t.Fatalf("DeliverPendingMessages: %v", err)
	}
	if len(delivered.Messages) != 1 {
		t.Fatalf("expected 1 delivered (the live publisher message), got %d", len(delivered.Messages))
	}
	if delivered.Messages[0].SenderKind != "publisher" {
		t.Errorf("delivered.sender_kind: got %s want publisher", delivered.Messages[0].SenderKind)
	}
}
