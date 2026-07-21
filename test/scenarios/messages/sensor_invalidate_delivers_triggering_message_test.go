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

func TestSensorInvalidateDeliversTriggeringMessage(t *testing.T) {
	t.Parallel()
	m := newFakeMessages()
	ctx := context.Background()
	instanceID := shared.UUID(uuid.New())
	frameID := shared.UUID(uuid.New())
	msgID := shared.UUID(uuid.New())
	now := time.Now().UTC()
	if err := runtime.EnqueueMessage(ctx, nil, &fakeEnqueueDeps{msgs: m}, persistence.EnqueueMessageRequest{
		ID:         msgID,
		InstanceID: instanceID,
		Type:       "invalidate",
		Sender:     "sensor-cron",
		SenderKind: "publisher",
		Payload:    []byte(`{"observed_at":"2026-05-15T12:00:00Z"}`),
		ReceivedAt: now,
	}); err != nil {
		t.Fatalf("EnqueueMessage: %v", err)
	}
	delivered, err := runtime.DeliverTriggeringMessage(ctx, nil, m, instanceID, frameID, msgID, now)
	if err != nil {
		t.Fatalf("DeliverTriggeringMessage: %v", err)
	}
	if len(delivered.Messages) != 1 || delivered.Messages[0].SenderKind != "publisher" {
		t.Errorf("expected one publisher-sender delivered, got %+v", delivered.Messages)
	}
}
