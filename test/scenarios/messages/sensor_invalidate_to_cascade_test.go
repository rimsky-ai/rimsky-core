// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N4 scenario — sensor_invalidate_to_cascade.
//
// A publisher (bundled sensor) POSTs a message envelope to the generic
// `POST /instances/{instance_id}/messages` endpoint with
// `sender_kind: "publisher"` + a `publisher_subscription_id` capability
// token. At frame boundary the scheduler delivers it. The scenario
// pins enqueue → deliver shape with sender_kind="publisher".
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

func TestSensorInvalidateToCascade(t *testing.T) {
	t.Parallel()
	m := newFakeMessages()
	ctx := context.Background()
	instanceID := shared.UUID(uuid.New())
	frameID := shared.UUID(uuid.New())
	now := time.Now().UTC()
	if err := runtime.EnqueueMessage(ctx, nil, m, persistence.EnqueueMessageRequest{
		ID:         shared.UUID(uuid.New()),
		InstanceID: instanceID,
		Kind:       "invalidate",
		Sender:     "sensor-cron",
		SenderKind: "publisher",
		Target:     "*",
		Payload:    []byte(`{"observed_at":"2026-05-15T12:00:00Z"}`),
		ReceivedAt: now,
	}); err != nil {
		t.Fatalf("EnqueueMessage: %v", err)
	}
	delivered, err := runtime.DeliverPendingMessages(ctx, nil, m, instanceID, frameID, runtime.FrameDeliveryCoalesce, now, nil)
	if err != nil {
		t.Fatalf("DeliverPendingMessages: %v", err)
	}
	if len(delivered.Messages) != 1 || delivered.Messages[0].SenderKind != "publisher" {
		t.Errorf("expected one publisher-sender delivered, got %+v", delivered.Messages)
	}
}
