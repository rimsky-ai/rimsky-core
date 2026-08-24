// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: terminal-resolution

package runtime

import (
	"context"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/service"
)

func abandonOpenedClaim(
	ctx context.Context,
	producer locks.ClaimProducer,
	claimHandleID shared.UUID,
	scope, address []byte,
	leaseToken string,
) error {
	claimID := claimproducer.ClaimID(claimHandleID.String())
	ctx = service.WithServiceName(ctx, producer.Name())
	return producer.Abandon(ctx, claimID, scope, address, leaseToken)
}
