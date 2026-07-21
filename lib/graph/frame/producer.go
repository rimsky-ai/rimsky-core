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

// @decision: empty-message-as-root-trigger
func openRunningFrameForMessage(
	ctx context.Context, store persistence.Tables, instanceID, triggeringMessageID uuid.UUID, tx persistence.Tx,
) (uuid.UUID, error) {

	// @concept: run-scope
	rootRunScopeID := shared.UUID(uuid.New())
	if err := store.RunScopes().Create(ctx, persistence.RunScopeRow{
		ID:           rootRunScopeID,
		GraphName:    spec.MainGraphName,
		InstanceID:   instanceID,
		PartitionKey: "",
	}, tx); err != nil {
		return uuid.Nil, fmt.Errorf("frame.openRunningFrameForMessage: create root run scope: %w", err)
	}
	return store.Frames().InsertRunningFrame(ctx, instanceID, triggeringMessageID, rootRunScopeID, tx)
}
