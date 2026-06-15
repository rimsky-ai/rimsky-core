// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package pgmigrate

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHarnessStartsPostgres(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, teardown := StartPostgres(ctx, t)
	t.Cleanup(teardown)

	// @deliberate: Basic smoke: query one of the migrated tables. Asserts at least one
	// migration applied (grows as new migrations land; currently 001 + 002).
	var count int
	err := pool.QueryRow(ctx, "SELECT count(*) FROM rimsky_migrations").Scan(&count)
	require.NoError(t, err)
	require.GreaterOrEqual(t, count, 1)
}
