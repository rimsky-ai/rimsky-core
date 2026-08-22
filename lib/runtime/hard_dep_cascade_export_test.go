// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import (
	"context"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	shared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
)

func CascadeSubscribersStaleInTxForTest(
	ctx context.Context, args RunArgs, senderID shared.UUID, senderNodeType string, senderNodeRunID shared.UUID, instanceID, senderFrameID shared.UUID, tx persistence.Tx,
) error {
	sig := signalpkg.BuildTerminalSuccessSignal(true, map[string]any{}, "cascade_test", nil)
	return cascadeSubscribersStaleInTx(ctx, args, senderID, senderNodeType, senderNodeRunID, instanceID, senderFrameID, sig, tx)
}

// @concept: auto-terminal
func TransitionHolderIfFullyResolvedForTest(
	ctx context.Context, args RunArgs, holderNodeRunID shared.UUID, tx persistence.Tx,
) (func(context.Context), error) {
	post, err := transitionHolderIfFullyResolved(ctx, args, holderNodeRunID, tx)
	if post == nil {
		return nil, err
	}
	return post, err
}
