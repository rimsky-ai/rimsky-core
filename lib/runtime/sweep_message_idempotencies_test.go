// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Coverage for SweepMessageIdempotencies's cutoff predicate.

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
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	pgtest "github.com/rimsky-ai/rimsky-core/test/support/pgmigrate"
)

func seedIdempotencyRow(ctx context.Context, t *testing.T, d persistence.Database, instanceID shared.UUID, createdAt time.Time) shared.UUID {
	t.Helper()
	msgID := shared.UUID(uuid.New())
	require.NoError(t, d.Tables().Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		_, _, err := d.Tables().MessageIdempotencies().InsertOrLookup(ctx, tx, persistence.MessageIdempotencyRow{
			InstanceID:     instanceID,
			SenderKind:     "operator",
			Sender:         "operator",
			IdempotencyKey: msgID.String(),
			MessageID:      msgID,
			CreatedAt:      createdAt,
		})
		return err
	}))
	if !createdAt.IsZero() {
		pool, ok := pgpersist.PoolFromDatabaseForTest(d)
		require.True(t, ok, "PoolFromDatabaseForTest failed")
		_, err := pool.Exec(ctx,
			`UPDATE rimsky_message_idempotencies SET created_at = $1 WHERE message_id = $2`,
			createdAt, msgID)
		require.NoError(t, err)
	}
	return msgID
}

func seedInstanceForIdempotencyTest(ctx context.Context, t *testing.T, d persistence.Database) shared.UUID {
	t.Helper()
	backend := d.Tables()
	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "idem-sweep-" + uuid.NewString(), Version: "1",
		Nodes: []node.TemplateNodeDef{{Type: "n", Executor: "stub"}},
	})
	var inst persistence.InstanceRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, _ := seedInstanceWithMainScope(ctx, t, backend, tx, tmpl.ID, nil)
		inst = i
		return nil
	}))
	return inst.ID
}

func TestSweepMessageIdempotencies_DeletesPastCutoff(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)

	instanceID := seedInstanceForIdempotencyTest(ctx, t, d)
	old := time.Now().Add(-25 * time.Hour)
	seedIdempotencyRow(ctx, t, d, instanceID, old)

	cfg := runtime.RetentionConfig{MessageIdempotenciesTrailing: 24 * time.Hour}
	n, err := runtime.SweepMessageIdempotencies(ctx, d.Tables().MessageIdempotencies(), cfg, time.Now(), shared.SilentLogger{})
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
}

func TestSweepMessageIdempotencies_PreservesWithinCutoff(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)

	instanceID := seedInstanceForIdempotencyTest(ctx, t, d)
	recent := time.Now().Add(-1 * time.Hour)
	seedIdempotencyRow(ctx, t, d, instanceID, recent)

	cfg := runtime.RetentionConfig{MessageIdempotenciesTrailing: 24 * time.Hour}
	n, err := runtime.SweepMessageIdempotencies(ctx, d.Tables().MessageIdempotencies(), cfg, time.Now(), shared.SilentLogger{})
	require.NoError(t, err)
	require.EqualValues(t, 0, n)
}

func TestSweepMessageIdempotencies_NoOpWhenEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)

	cfg := runtime.RetentionConfig{MessageIdempotenciesTrailing: 24 * time.Hour}
	n, err := runtime.SweepMessageIdempotencies(ctx, d.Tables().MessageIdempotencies(), cfg, time.Now(), shared.SilentLogger{})
	require.NoError(t, err)
	require.EqualValues(t, 0, n)
}
