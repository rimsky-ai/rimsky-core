// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
	ctx context.Context, args RunArgs, senderNodeID foundationshared.UUID, senderNodeType string, senderNodeRunID foundationshared.UUID, instanceID foundationshared.UUID, senderFrameID foundationshared.UUID, changeSummary string, tx persistence.Tx,
) error {
	successSig := signalpkg.BuildTerminalSuccessSignal(false, map[string]any{}, changeSummary, nil)
	if err := emitSignalInTxOnce(ctx, args, senderNodeID, senderNodeType, senderNodeRunID, instanceID, senderFrameID, successSig, tx); err != nil {
		return err
	}
	if err := upsertDataFromDispatchInputBagIfEmpty(ctx, args, senderNodeRunID, senderNodeID, tx); err != nil {
		return err
	}
	if err := emitAttributeChangesForRunInTx(ctx, args, senderNodeID, senderNodeType, senderNodeRunID, instanceID, senderFrameID, nil, nil, tx); err != nil {
		return err
	}
	return drainWaitSetOnSettled(ctx, args, senderFrameID, senderNodeRunID, tx)
}
