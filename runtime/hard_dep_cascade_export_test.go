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
)

// CascadeSubscribersStaleInTxForTest invokes the unexported
// `cascadeSubscribersStaleInTx`. The signature mirrors the internal
// helper one-to-one.
func CascadeSubscribersStaleInTxForTest(
	ctx context.Context, args RunArgs, tx persistence.Tx,
	senderID shared.UUID, senderNodeType string, senderRunID shared.UUID,
	instanceID, senderFrameID shared.UUID,
) error {
	return cascadeSubscribersStaleInTx(ctx, args, tx, senderID, senderNodeType, senderRunID, instanceID, senderFrameID)
}
