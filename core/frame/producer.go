package frame

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/core/persistence"
)

// EnqueueOrCoalesce inserts (serial_queue) or upserts (coalesce) a queued
// frame for the instance. The caller passes a tx so the enqueue can join
// the producer's existing transaction (e.g., the schedule_ticker's tick tx,
// or the controlapi handler's request tx).
//
// Returns the frame_id of the row that received the source — either a
// freshly-created row or an existing pending-coalesce row.
//
// @blessed-invariant 15 (mode mandatory): the helper reads mode from the
// template join and rejects if missing.
func EnqueueOrCoalesce(ctx context.Context, store persistence.Store, tx persistence.Tx,
	instanceID, sourceNodeID uuid.UUID) (uuid.UUID, error) {

	mode, frameTimeoutMs, err := store.Frames().LookupFrameMode(ctx, instanceID, tx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("frame.EnqueueOrCoalesce: %w", err)
	}

	switch mode {
	case persistence.FrameModeSerialQueue:
		return store.Frames().EnqueueSerialFrame(ctx, instanceID, sourceNodeID, frameTimeoutMs, tx)
	case persistence.FrameModeCoalesce:
		return store.Frames().EnqueueCoalesceFrame(ctx, instanceID, sourceNodeID, frameTimeoutMs, tx)
	default:
		return uuid.Nil, fmt.Errorf("frame.EnqueueOrCoalesce: unsupported mode %q for instance %s",
			mode, instanceID)
	}
}
