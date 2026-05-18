// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N4 scenario — dead_letter.
//
// A cancelled message (col:rimsky_messages.cancelled = TRUE) is
// stamped delivered_at with frame_id=NULL by the backfill
// cancellation path. The delivery layer skips cancelled rows so
// they do not appear in DeliverPendingMessages output. The
// scenario pins this filter — the cancelled row remains in the
// table for diagnostics but is not redelivered.
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

func TestDeadLetter_CancelledNotDelivered(t *testing.T) {
	t.Parallel()
	m := newFakeMessages()
	ctx := context.Background()
	instanceID := shared.UUID(uuid.New())
	frameID := shared.UUID(uuid.New())
	now := time.Now().UTC()
	backfillOp := shared.UUID(uuid.New())

	// One live message + one cancelled-on-backfill-rollback message.
	if err := runtime.EnqueueMessage(ctx, nil, m, persistence.EnqueueMessageRequest{
		ID:                  shared.UUID(uuid.New()),
		InstanceID:          instanceID,
		Kind:                "invalidate",
		Sender:              "operator/main",
		SenderKind:          "operator",
		BackfillOperationID: &backfillOp,
		ReceivedAt:          now,
	}); err != nil {
		t.Fatalf("EnqueueMessage cancelled: %v", err)
	}
	if err := runtime.EnqueueMessage(ctx, nil, m, persistence.EnqueueMessageRequest{
		ID:         shared.UUID(uuid.New()),
		InstanceID: instanceID,
		Kind:       "invalidate",
		Sender:     "sensor-cron",
		SenderKind: "publisher",
		ReceivedAt: now,
	}); err != nil {
		t.Fatalf("EnqueueMessage live: %v", err)
	}
	// Cancel the backfill — should mark cancelled=true on the first row.
	if n, err := m.MarkCancelled(ctx, nil, backfillOp, now); err != nil || n != 1 {
		t.Fatalf("MarkCancelled: n=%d err=%v", n, err)
	}
	delivered, err := runtime.DeliverPendingMessages(ctx, nil, m, instanceID, frameID, runtime.FrameDeliveryCoalesce, now)
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
