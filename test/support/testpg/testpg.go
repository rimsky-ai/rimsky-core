// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package testpg

import (
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/pgpool"
)

var sharedPool = pgpool.NewRimskySchemaPool()

func StartFreshPostgresDSN(ctx context.Context, t *testing.T) (string, func()) {
	t.Helper()
	return sharedPool.Acquire(ctx, t), func() {}
}
