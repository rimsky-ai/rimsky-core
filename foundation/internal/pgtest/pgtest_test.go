package pgtest

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

	// Basic smoke: query one of the migrated tables. Asserts at least one
	// migration applied (grows as new migrations land; currently 001 + 002).
	var count int
	err := pool.QueryRow(ctx, "SELECT count(*) FROM rimsky_migrations").Scan(&count)
	require.NoError(t, err)
	require.GreaterOrEqual(t, count, 1)
}
