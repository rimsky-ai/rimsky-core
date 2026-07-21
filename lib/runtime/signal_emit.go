// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	foundationshared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	signalaudit "github.com/rimsky-ai/rimsky-core/lib/foundation/signal/audit"
)

// @concept: cascade
// @concept: signal
// @concept: wait-set
func emitSignalInTx(
	ctx context.Context, args RunArgs, senderID foundationshared.UUID, senderNodeType string, senderNodeRunID foundationshared.UUID, instanceID foundationshared.UUID, senderFrameID foundationshared.UUID, sig signalpkg.Signal, visited map[foundationshared.UUID]struct{}, tx persistence.Tx,
) error {
	return emitSignalInTxWithFilter(ctx, args, senderID, senderNodeType, senderNodeRunID, instanceID, senderFrameID, sig, visited, nil, tx)
}

// @concept: cascade
// @decision: held-as-state-not-phase
func emitSignalInTxWithFilter(
	ctx context.Context, args RunArgs, senderID foundationshared.UUID, senderNodeType string, senderNodeRunID foundationshared.UUID, instanceID foundationshared.UUID, senderFrameID foundationshared.UUID, sig signalpkg.Signal, visited map[foundationshared.UUID]struct{}, filter receiverFilter, tx persistence.Tx,
) error {
	if err := cascadeSignalInTxWithFilter(ctx, args, senderID, senderNodeType, senderNodeRunID, instanceID, senderFrameID, sig, visited, filter, tx); err != nil {
		return err
	}
	return signalaudit.EmitSignal(ctx, args.Persist.Events(),
		instanceID, senderID, sig, args.Clock.Now(), tx)
}

// @concept: cascade
// @decision: held-as-state-not-phase
func cascadeSignalInTxWithFilter(
	ctx context.Context, args RunArgs, senderID foundationshared.UUID, senderNodeType string, senderNodeRunID foundationshared.UUID, instanceID foundationshared.UUID, senderFrameID foundationshared.UUID, sig signalpkg.Signal, visited map[foundationshared.UUID]struct{}, filter receiverFilter, tx persistence.Tx,
) error {
	if err := signalpkg.ValidateTypePath(sig.Type); err != nil {
		return fmt.Errorf("cascadeSignalInTxWithFilter: %w", err)
	}
	var zeroUUID foundationshared.UUID
	if senderNodeRunID != zeroUUID && senderFrameID != zeroUUID {
		if err := cascadeSubscribersStaleInTxWithVisited(ctx, args, senderID, senderNodeType, senderNodeRunID, instanceID, senderFrameID, sig, visited, filter, tx); err != nil {
			return err
		}
	}
	return nil
}

func emitSignalInTxOnce(
	ctx context.Context, args RunArgs, senderID foundationshared.UUID, senderNodeType string, senderNodeRunID foundationshared.UUID, instanceID foundationshared.UUID, senderFrameID foundationshared.UUID, sig signalpkg.Signal, tx persistence.Tx,
) error {
	return emitSignalInTx(ctx, args, senderID, senderNodeType, senderNodeRunID, instanceID, senderFrameID, sig, map[foundationshared.UUID]struct{}{}, tx)
}
