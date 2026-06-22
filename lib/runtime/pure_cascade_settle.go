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
	senderRunID foundationshared.UUID,
	instanceID foundationshared.UUID,
	senderFrameID foundationshared.UUID,
	changeSummary string,
) error {
	successSig := signalpkg.Signal{
		Type: signalpkg.TypePath("terminal/success"),
		Payload: map[string]any{
			"changed":          false,
			"attributes_delta": map[string]any{},
			"change_summary":   changeSummary,
		},
	}
	if err := emitSignalInTxOnce(ctx, args, tx,
		senderNodeID, senderNodeType, senderRunID, instanceID, senderFrameID, successSig); err != nil {
		return err
	}
	return drainWaitSetOnSettled(ctx, args, tx, senderFrameID, senderRunID)
}
