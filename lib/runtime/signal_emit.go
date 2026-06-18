// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	foundationshared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	signalaudit "github.com/rimsky-ai/rimsky-core/lib/foundation/signal/audit"
)

// @concept: cascade
// @concept: signal
// @concept: wait-set
func emitSignalInTx(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	senderID foundationshared.UUID,
	senderNodeType string,
	senderRunID foundationshared.UUID,
	instanceID foundationshared.UUID,
	senderFrameID foundationshared.UUID,
	sig signalpkg.Signal,
	visited map[foundationshared.UUID]struct{},
) error {
	var zeroUUID foundationshared.UUID
	if senderRunID != zeroUUID && senderFrameID != zeroUUID {
		if err := cascadeSubscribersStaleInTxWithVisited(ctx, args, tx,
			senderID, senderNodeType, senderRunID, instanceID, senderFrameID, sig, visited); err != nil {
			return err
		}
	}
	return signalaudit.EmitSignal(ctx, args.Persist.Events(),
		instanceID, senderID, sig, args.Clock.Now(), tx)
}

func emitSignalInTxOnce(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	senderID foundationshared.UUID,
	senderNodeType string,
	senderRunID foundationshared.UUID,
	instanceID foundationshared.UUID,
	senderFrameID foundationshared.UUID,
	sig signalpkg.Signal,
) error {
	return emitSignalInTx(ctx, args, tx, senderID, senderNodeType, senderRunID,
		instanceID, senderFrameID, sig, map[foundationshared.UUID]struct{}{})
}
