// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

func TestOperatorInvalidateDeliversTriggeringMessage(t *testing.T) {
	t.Parallel()
	m := newFakeMessages()
	ctx := context.Background()

	instanceID := shared.UUID(uuid.New())
	frameID := shared.UUID(uuid.New())
	msgID := shared.UUID(uuid.New())

	now := time.Now().UTC()
	if err := runtime.EnqueueMessage(ctx, &fakeEnqueueDeps{msgs: m}, persistence.EnqueueMessageRequest{
		ID:         msgID,
		InstanceID: instanceID,
		Type:       "invalidate",
		Sender:     "operator/admin",
		SenderKind: "operator",
		ReceivedAt: now,
	}, nil); err != nil {
		t.Fatalf("EnqueueMessage: %v", err)
	}
	delivered, err := runtime.DeliverTriggeringMessage(ctx, m, instanceID, frameID, msgID, now, nil)
	if err != nil {
		t.Fatalf("DeliverTriggeringMessage: %v", err)
	}
	if len(delivered.Messages) != 1 {
		t.Fatalf("expected 1 delivered, got %d", len(delivered.Messages))
	}
	got := delivered.Messages[0]
	if got.ID != msgID {
		t.Errorf("delivered id %s want %s", got.ID, msgID)
	}
	if got.FrameID == nil || *got.FrameID != frameID {
		t.Errorf("delivered frame_id mismatch: got %v want %s", got.FrameID, frameID)
	}
	if got.SenderKind != "operator" {
		t.Errorf("sender_kind %s want operator", got.SenderKind)
	}
}
