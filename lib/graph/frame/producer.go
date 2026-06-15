// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package frame

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

// EnqueueFrame inserts a queued frame for the given instance triggered by
// the supplied message envelope and returns the new frame_id. The caller
// passes a tx so the enqueue can join the producer's existing transaction
// (the control-API handler's request tx, the runtime cascade tx, etc.).
//
// One-message-per-frame is the only delivery shape under the
// message-schema-layer redesign — there is no coalesce branch, no
// resolution-mode dispatch, no upsert. The persistence call (InsertFrame)
// is a plain INSERT; the frame→message FK is ON DELETE RESTRICT — a
// triggering message cannot be deleted while a frame points at it, so a
// future message reaper cannot silently destroy a frame's origin row.
// Instance-wide teardown still works because instance delete CASCADEs
// both rimsky_frames.instance_id and rimsky_messages.instance_id in
// parallel.
func EnqueueFrame(ctx context.Context, store persistence.Tables, tx persistence.Tx,
	instanceID, triggeringMessageID uuid.UUID) (uuid.UUID, error) {

	frameTimeoutMs, err := store.Frames().LookupFrameTimeoutMs(ctx, instanceID, tx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("frame.EnqueueFrame: %w", err)
	}
	return store.Frames().InsertFrame(ctx, instanceID, triggeringMessageID, frameTimeoutMs, tx)
}
