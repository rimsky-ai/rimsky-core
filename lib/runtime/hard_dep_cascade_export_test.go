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
	sig := signalpkg.Signal{
		Type: "terminal/success",
		Payload: map[string]any{
			"changed":          true,
			"attributes_delta": map[string]any{},
			"change_summary":   "cascade_test",
		},
	}
	return cascadeSubscribersStaleInTx(ctx, args, senderID, senderNodeType, senderNodeRunID, instanceID, senderFrameID, sig, tx)
}
