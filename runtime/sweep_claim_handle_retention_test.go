// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Coverage for SweepClaimHandleRetention's cutoff + exempt-from-sweep
// predicates. Seeds claim_handle rows in known states and asserts the
// sweep deletes/preserves per the documented policy.

package runtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	pgpersist "github.com/fallguyconsulting/rimsky/foundation/persistence/postgres"
	"github.com/fallguyconsulting/rimsky/foundation/shared"
	"github.com/fallguyconsulting/rimsky/foundation/spec"
	"github.com/fallguyconsulting/rimsky/graph/node"
	pgtest "github.com/fallguyconsulting/rimsky/internal/pgmigrate"
	"github.com/fallguyconsulting/rimsky/runtime"
)

// seedClaimHandleForSweep inserts a claim_handle row, optionally
// promotes it to the given state, and (when resolvedAt is non-zero)
// backdates the `resolved_at` column directly via SQL so the row
// looks "old" to the retention sweep.
func seedClaimHandleForSweep(
	ctx context.Context, t *testing.T, d persistence.Database,
	supervisorID string, lifetime spec.ClaimLifetime,
	promote spec.ClaimHandleState, resolvedAt time.Time,
) shared.UUID {
	t.Helper()
	backend := d.Tables()

	// Need an instance + node to anchor the row's FKs.
	tmpl := insertDeployedTemplate(ctx, t, backend, node.TemplateSpec{
		Name: "ret-sweep-" + uuid.NewString(), Version: "1",
		Nodes: []node.TemplateNodeDef{{Type: "n", Executor: "stub"}},
	})
	var inst persistence.InstanceRow
	var nd persistence.NodeRow
	require.NoError(t, backend.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		i, _ := seedInstanceWithMainScope(ctx, t, backend, tx, tmpl.ID, nil)
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
		// Promote to the requested terminal state (if any).
		if promote != "" && promote != spec.ClaimHandleStateActive {
			if err := backend.ClaimHandles().Promote(ctx, chID, supervisorID, promote, tx); err != nil {
				return err
			}
		}
		return nil
	}))

	if !resolvedAt.IsZero() {
		// Backdate resolved_at directly via SQL.
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
	d := pgtest.OpenDriver(ctx, t)

	// Durable + committed row with resolved_at well in the past.
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

// Notes (diagnostic — testcontainer-startup-bound, not a
// production-code bug):
//
//	Symptom: under heavy parallel load (full
//	./foundation/persistence/... + ./runtime/... runs with -race +
//	-parallel=N), this test occasionally hits the default `go test`
//	per-test timeout (10m unless overridden) — not because the test
//	logic is slow, but because pgtest.OpenDriver below has to spin
//	up a fresh postgres:14-alpine container and the per-poll Docker
//	state-query can spike to 15-20s under contention.
//
//	Ruled out: the production code under test —
//	runtime.SweepClaimHandleRetention is a single synchronous SQL
//	DELETE with a deterministic predicate
//	(state IN ('committed','abandoned') AND lifetime='subgraph' AND
//	resolved_at < $cutoff). The test calls it inline; there is no
//	scheduler, no supervisor, no executor, and no polling loop.
//	Once OpenDriver returns the latency to assertion is sub-second.
//
//	Root cause located: testcontainer cold-start latency in
//	sdk/go/testpg/testpg.go::StartFreshPostgresDSN. That helper
//	already documents the 300s container-startup ceiling and the
//	"~1-6s per Docker poll under saturated parallel load;
//	occasional 15-20s spikes" envelope; under -parallel=N the
//	container starts compete for the same docker socket.
//
//	Resolution: rely on `go test -timeout` (10m default) plus the
//	300s wait-strategy ceiling inside StartFreshPostgresDSN. No
//	in-test deadline is set here because the work itself is
//	bounded — adding one would only mask a real container-startup
//	regression. If a future flake recurs here, look at docker
//	daemon health and parallel-container saturation, not at the
//	sweep predicate.
func TestSweepClaimHandleRetention_SweepsSubgraphCommittedPastCutoff(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := pgtest.OpenDriver(ctx, t)

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
	d := pgtest.OpenDriver(ctx, t)

	oneYearAgo := time.Now().Add(-365 * 24 * time.Hour)
	// Abandoned rows are swept regardless of lifetime — try durable to
	// confirm.
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
	d := pgtest.OpenDriver(ctx, t)

	// resolved_at = 1 hour ago, within the 30-day cutoff.
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
	d := pgtest.OpenDriver(ctx, t)

	// Active row — resolved_at is NULL on active rows; the sweep
	// predicate filters them out via `state IN ('committed','abandoned')`
	// AND `resolved_at < cutoff`. Defense in depth.
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
	d := pgtest.OpenDriver(ctx, t)

	// Seed a row that would otherwise be swept.
	oneYearAgo := time.Now().Add(-365 * 24 * time.Hour)
	seedClaimHandleForSweep(ctx, t, d, "sup-6",
		spec.ClaimLifetimeSubgraph, spec.ClaimHandleStateCommitted, oneYearAgo)

	cfg := runtime.RetentionConfig{ClaimHandlesTrailing: 0}
	n, err := runtime.SweepClaimHandleRetention(ctx, d.Tables().ClaimHandles(), cfg, time.Now(), shared.SilentLogger{})
	require.NoError(t, err)
	require.Equal(t, 0, n, "zero trailing must disable the sweep")
}

// getClaimHandleByID returns the row or nil if gone.
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
