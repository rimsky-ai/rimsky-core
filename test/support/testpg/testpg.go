// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package testpg

import (
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/pgpool"
)

var sharedPool = pgpool.NewRimskySchemaPool()

func StartFreshPostgresDSN(ctx context.Context, t *testing.T) string {
	t.Helper()
	return sharedPool.Acquire(ctx, t)
}
