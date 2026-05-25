// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package frame

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/fallguyconsulting/rimsky/foundation/persistence"
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
func EnqueueOrCoalesce(ctx context.Context, store persistence.Tables, tx persistence.Tx,
	instanceID, sourceNodeID uuid.UUID) (uuid.UUID, error) {

	mode, frameTimeoutMs, err := store.Frames().LookupFrameResolutionMode(ctx, instanceID, tx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("frame.EnqueueOrCoalesce: %w", err)
	}

	switch mode {
	case persistence.FrameResolutionModeSerialQueue:
		return store.Frames().EnqueueSerialFrame(ctx, instanceID, sourceNodeID, frameTimeoutMs, tx)
	case persistence.FrameResolutionModeCoalesce:
		return store.Frames().EnqueueCoalesceFrame(ctx, instanceID, sourceNodeID, frameTimeoutMs, tx)
	default:
		return uuid.Nil, fmt.Errorf("frame.EnqueueOrCoalesce: unsupported mode %q for instance %s",
			mode, instanceID)
	}
}
