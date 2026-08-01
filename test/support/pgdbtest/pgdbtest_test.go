// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package pgdbtest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHarnessStartsPostgres(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := StartPostgres(ctx, t)

	var count int
	err := pool.QueryRow(ctx, "SELECT count(*) FROM rimsky_migrations").Scan(&count)
	require.NoError(t, err)
	require.GreaterOrEqual(t, count, 1)
}
