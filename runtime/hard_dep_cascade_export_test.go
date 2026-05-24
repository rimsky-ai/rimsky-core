// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Test-only exports for the unexported cascadeSubscribersStaleInTx so
// the external `runtime_test` package can exercise the cascade walk
// directly. This file's `*_test.go` suffix keeps the symbols out of
// production builds.

package runtime

import (
	"context"

	"github.com/fallguy/rimsky/foundation/persistence"
	shared "github.com/fallguy/rimsky/foundation/shared"
	signalpkg "github.com/fallguy/rimsky/foundation/signal"
)

// CascadeSubscribersStaleInTxForTest invokes the unexported
// `cascadeSubscribersStaleInTx` with a synthetic terminal/success
// signal (changed: true) so callers that don't care about CEL filter
// details get the test-default "fire all matching subscribers"
// behavior. Callers needing a specific signal shape should compose
// their own envelope.
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
