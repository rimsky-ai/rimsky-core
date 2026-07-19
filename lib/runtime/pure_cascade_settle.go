// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	foundationshared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
)

// @concept: cascade
// @concept: signal
// @concept: wait-set
func EmitTerminalSuccessAndDrainInTx(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	senderNodeID foundationshared.UUID,
	senderNodeType string,
	senderNodeRunID foundationshared.UUID,
	instanceID foundationshared.UUID,
	senderFrameID foundationshared.UUID,
	changeSummary string,
) error {
	successSig := signalpkg.BuildTerminalSuccessSignal(false, map[string]any{}, changeSummary, nil)
	if err := emitSignalInTxOnce(ctx, args, tx,
		senderNodeID, senderNodeType, senderNodeRunID, instanceID, senderFrameID, successSig); err != nil {
		return err
	}
	if err := upsertDataFromDispatchInputBagIfEmpty(ctx, args, tx, senderNodeRunID, senderNodeID); err != nil {
		return err
	}
	if err := emitAttributeChangesForRunInTx(ctx, args, tx,
		senderNodeID, senderNodeType, senderNodeRunID, instanceID, senderFrameID,
		nil, nil); err != nil {
		return err
	}
	return drainWaitSetOnSettled(ctx, args, tx, senderFrameID, senderNodeRunID)
}
