// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: terminal-resolution

package runtime

import (
	"context"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

func abandonOpenedClaim(
	ctx context.Context,
	producer locks.ClaimProducer,
	claimHandleID shared.UUID,
	scope, address []byte,
	leaseToken string,
) error {
	claimID := claimproducer.ClaimID(claimHandleID.String())
	ctx = peer.WithServiceName(ctx, producer.Name())
	return producer.Abandon(ctx, claimID, scope, address, leaseToken)
}
