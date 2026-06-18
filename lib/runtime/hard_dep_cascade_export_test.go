// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"context"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	shared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
)

func CascadeSubscribersStaleInTxForTest(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	senderID shared.UUID, senderNodeType string, senderRunID shared.UUID,
	instanceID, senderFrameID shared.UUID,
) error {
	sig := signalpkg.Signal{
		Type: "terminal/success",
		Payload: map[string]any{
			"changed":          true,
			"attributes_delta": map[string]any{},
			"change_summary":   "cascade_test",
		},
	}
	return cascadeSubscribersStaleInTx(ctx, args, tx, senderID, senderNodeType, senderRunID, instanceID, senderFrameID, sig)
}
