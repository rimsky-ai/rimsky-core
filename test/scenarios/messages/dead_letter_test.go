// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package messages

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

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
	deps := &fakeEnqueueDeps{msgs: m, queueModes: map[shared.UUID]string{instanceID: "coalesce"}}

	staleID := shared.UUID(uuid.New())
	require.NoError(t, runtime.EnqueueMessage(ctx, deps, persistence.EnqueueMessageRequest{
		ID:         staleID,
		InstanceID: instanceID,
		Type:       "invalidate",
		Sender:     "sensor-cron",
		SenderKind: "publisher",
		ReceivedAt: now,
	}, nil))
	liveID := shared.UUID(uuid.New())
	require.NoError(t, runtime.EnqueueMessage(ctx, deps, persistence.EnqueueMessageRequest{
		ID:         liveID,
		InstanceID: instanceID,
		Type:       "invalidate",
		Sender:     "sensor-cron",
		SenderKind: "publisher",
		ReceivedAt: now.Add(time.Second),
	}, nil))

	stale, err := m.Get(ctx, staleID, nil)
	require.NoError(t, err)
	require.True(t, stale.Cancelled, "coalesce receipt must cancel the superseded pending message")
	require.Nil(t, stale.DeliveredAt, "a cancelled message must never be marked delivered")

	pending, err := m.ListPendingForInstance(ctx, instanceID, nil)
	require.NoError(t, err)
	require.Len(t, pending, 1, "the cancelled message must not surface as pending")
	require.Equal(t, liveID, pending[0].ID)

	deadLettered, err := runtime.DeliverTriggeringMessage(ctx, m, instanceID, frameID, staleID, now, nil)
	require.NoError(t, err)
	require.Empty(t, deadLettered.Messages, "a cancelled message must never be delivered, even if named as a frame's trigger")

	delivered, err := runtime.DeliverTriggeringMessage(ctx, m, instanceID, frameID, liveID, now, nil)
	require.NoError(t, err)
	require.Len(t, delivered.Messages, 1, "the surviving message must still deliver")
	require.Equal(t, "publisher", delivered.Messages[0].SenderKind)
}
