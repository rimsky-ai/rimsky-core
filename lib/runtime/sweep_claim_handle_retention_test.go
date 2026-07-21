// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	pgpersist "github.com/rimsky-ai/rimsky-core/lib/foundation/persistence/postgres"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/test/support/pgdbtest"
)

func seedClaimHandleForSweep(
	ctx context.Context, t *testing.T, d persistence.Database,
	supervisorID string, lifetime spec.ClaimLifetime,
	promote spec.ClaimHandleState, resolvedAt time.Time,
) shared.UUID {
	t.Helper()
	backend := d.Tables()

	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "ret-sweep-" + uuid.NewString(), Version: "1",
		Nodes: []node.TemplateNodeDef{{Type: "n", Executor: "stub"}},
	})
	var inst persistence.InstanceRow
	var nd persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, _ := seedInstanceWithMainScope(ctx, t, backend, tmpl.ID, nil, tx)
		inst = i
		n, err := backend.Nodes().Create(ctx, persistence.NodeCreateInput{
			ID: shared.UUID(uuid.New()), InstanceID: inst.ID, NodeType: "n", Executor: "stub",
		}, tx)
		if err != nil {
			return err
		}
		nd = n
		return nil
	}))

	chID := shared.UUID(uuid.New())
	intent := "rw"
	producer := "test-producer"
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		if err := backend.ClaimHandles().Insert(ctx, persistence.ClaimHandleInsertInput{
			ID:                 chID,
			LockKind:           persistence.LockKindScope,
			ProducerName:       &producer,
			ClaimScopeData:     []byte(`"scope-` + chID.String() + `"`),
			Address:            []byte(`"addr"`),
			Intent:             &intent,
			HolderSupervisorID: supervisorID,
			HolderNodeID:       nd.ID,
			ExpiresAt:          time.Now().Add(10 * time.Minute),
			Lifetime:           lifetime,
		}, tx); err != nil {
			return err
		}
		if promote != "" && promote != spec.ClaimHandleStateActive {
			if err := backend.ClaimHandles().Promote(ctx, chID, supervisorID, promote, tx); err != nil {
				return err
			}
		}
		return nil
	}))

	if !resolvedAt.IsZero() {
		pool, ok := pgpersist.PoolFromDatabaseForTest(d)
		require.True(t, ok, "PoolFromDatabaseForTest failed")
		_, err := pool.Exec(ctx,
			`UPDATE rimsky_claim_handles SET resolved_at = $1 WHERE id = $2`,
			resolvedAt, chID)
		require.NoError(t, err)
	}
	return chID
}

func TestSweepClaimHandleRetention_DoesNotSweepDurableCommitted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)

	oneYearAgo := time.Now().Add(-365 * 24 * time.Hour)
	id := seedClaimHandleForSweep(ctx, t, d, "sup-1",
		spec.ClaimLifetimeDurable, spec.ClaimHandleStateCommitted, oneYearAgo)

	cfg := runtime.RetentionConfig{ClaimHandlesTrailing: 30 * 24 * time.Hour}
	n, err := runtime.SweepClaimHandleRetention(ctx, d.Tables().ClaimHandles(), cfg, time.Now(), shared.SilentLogger{})
	require.NoError(t, err)
	require.Equal(t, 0, n, "durable-committed rows must not be swept")

	row := getClaimHandleByID(ctx, t, d, id)
	require.NotNil(t, row, "durable-committed row must still exist post-sweep")
	require.Equal(t, spec.ClaimHandleStateCommitted, row.State)
}

func TestSweepClaimHandleRetention_SweepsSubgraphCommittedPastCutoff(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)

	oneYearAgo := time.Now().Add(-365 * 24 * time.Hour)
	id := seedClaimHandleForSweep(ctx, t, d, "sup-2",
		spec.ClaimLifetimeSubgraph, spec.ClaimHandleStateCommitted, oneYearAgo)

	cfg := runtime.RetentionConfig{ClaimHandlesTrailing: 30 * 24 * time.Hour}
	n, err := runtime.SweepClaimHandleRetention(ctx, d.Tables().ClaimHandles(), cfg, time.Now(), shared.SilentLogger{})
	require.NoError(t, err)
	require.Equal(t, 1, n, "subgraph-committed row past cutoff must be swept")

	row := getClaimHandleByID(ctx, t, d, id)
	require.Nil(t, row, "swept row must be gone")
}

func TestSweepClaimHandleRetention_SweepsAbandonedPastCutoff(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)

	oneYearAgo := time.Now().Add(-365 * 24 * time.Hour)
	id := seedClaimHandleForSweep(ctx, t, d, "sup-3",
		spec.ClaimLifetimeDurable, spec.ClaimHandleStateAbandoned, oneYearAgo)

	cfg := runtime.RetentionConfig{ClaimHandlesTrailing: 30 * 24 * time.Hour}
	n, err := runtime.SweepClaimHandleRetention(ctx, d.Tables().ClaimHandles(), cfg, time.Now(), shared.SilentLogger{})
	require.NoError(t, err)
	require.Equal(t, 1, n, "abandoned row past cutoff must be swept (any lifetime)")

	row := getClaimHandleByID(ctx, t, d, id)
	require.Nil(t, row)
}

func TestSweepClaimHandleRetention_DoesNotSweepWithinCutoff(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)

	oneHourAgo := time.Now().Add(-1 * time.Hour)
	id := seedClaimHandleForSweep(ctx, t, d, "sup-4",
		spec.ClaimLifetimeSubgraph, spec.ClaimHandleStateCommitted, oneHourAgo)

	cfg := runtime.RetentionConfig{ClaimHandlesTrailing: 30 * 24 * time.Hour}
	n, err := runtime.SweepClaimHandleRetention(ctx, d.Tables().ClaimHandles(), cfg, time.Now(), shared.SilentLogger{})
	require.NoError(t, err)
	require.Equal(t, 0, n)

	row := getClaimHandleByID(ctx, t, d, id)
	require.NotNil(t, row, "row within cutoff must survive")
}

func TestSweepClaimHandleRetention_DoesNotSweepActive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)

	id := seedClaimHandleForSweep(ctx, t, d, "sup-5",
		spec.ClaimLifetimeSubgraph, spec.ClaimHandleStateActive, time.Time{})

	cfg := runtime.RetentionConfig{ClaimHandlesTrailing: 30 * 24 * time.Hour}
	n, err := runtime.SweepClaimHandleRetention(ctx, d.Tables().ClaimHandles(), cfg, time.Now(), shared.SilentLogger{})
	require.NoError(t, err)
	require.Equal(t, 0, n)

	row := getClaimHandleByID(ctx, t, d, id)
	require.NotNil(t, row)
	require.Equal(t, spec.ClaimHandleStateActive, row.State)
}

func TestSweepClaimHandleRetention_DisabledByZeroTrailing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgdbtest.OpenDriver(ctx, t)

	oneYearAgo := time.Now().Add(-365 * 24 * time.Hour)
	seedClaimHandleForSweep(ctx, t, d, "sup-6",
		spec.ClaimLifetimeSubgraph, spec.ClaimHandleStateCommitted, oneYearAgo)

	cfg := runtime.RetentionConfig{ClaimHandlesTrailing: 0}
	n, err := runtime.SweepClaimHandleRetention(ctx, d.Tables().ClaimHandles(), cfg, time.Now(), shared.SilentLogger{})
	require.NoError(t, err)
	require.Equal(t, 0, n, "zero trailing must disable the sweep")
}

func getClaimHandleByID(ctx context.Context, t *testing.T, d persistence.Database, id shared.UUID) *persistence.ClaimHandleRow {
	t.Helper()
	var row *persistence.ClaimHandleRow
	require.NoError(t, d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := d.Tables().ClaimHandles().Get(ctx, id, tx)
		row = r
		return err
	}))
	return row
}
