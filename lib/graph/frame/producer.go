// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package frame

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func EnqueueFrame(ctx context.Context, store persistence.Tables, tx persistence.Tx,
	instanceID, triggeringMessageID uuid.UUID) (uuid.UUID, error) {

	frameTimeoutMs, err := store.Frames().LookupFrameTimeoutMs(ctx, instanceID, tx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("frame.EnqueueFrame: %w", err)
	}
	// @concept: run-scope
	rootRunScopeID := shared.UUID(uuid.New())
	if err := store.RunScopes().Create(ctx, tx, persistence.RunScopeRow{
		ID:           rootRunScopeID,
		GraphName:    spec.MainGraphName,
		InstanceID:   instanceID,
		PartitionKey: "",
	}); err != nil {
		return uuid.Nil, fmt.Errorf("frame.EnqueueFrame: create root run scope: %w", err)
	}
	return store.Frames().InsertFrame(ctx, instanceID, triggeringMessageID, rootRunScopeID, frameTimeoutMs, tx)
}
