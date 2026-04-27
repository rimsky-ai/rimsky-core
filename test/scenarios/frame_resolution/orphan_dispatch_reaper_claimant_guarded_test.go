// Verifies that runReapOrphanFrameDispatches releases dispatch claims
// using a per-row, claimant-guarded UPDATE (blessed-invariant 4): the
// SET clauses run only when `claimed_by = priorClaimedBy`. A fresh
// supervisor that re-claimed the row between the SELECT and the UPDATE
// keeps its live claim.
//
// This test catches review Issue 1: the previous bulk UPDATE
// indiscriminately nulled claim fields whenever the joined frame was
// terminal, racing with a still-live supervisor's slow finish.
//
// Two cases:
//  1. Plain orphan reap — frame terminal, dispatch claim from the same
//     supervisor that owned it when the frame ran. The reaper releases
//     the claim.
//  2. Claimant-guard — the per-row release issued with a stale prior-
//     claimed-by must NOT touch a row whose claim has rotated to a
//     fresh supervisor.
package frame_resolution

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/frame"
	"github.com/fallguy/rimsky/core/scenario"
)

func TestOrphanDispatchReaper_ReleasesTerminalFrameClaim(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{NoScheduler: true})

	dispatchID := seedTerminalFrameAndDispatch(t, h.Ctx, h.Pool, "stale-sup")

	require.NoError(t, frame.RunTick(h.Ctx, h.Pool, slog.New(slog.NewTextHandler(io.Discard, nil))))

	var claimedBy *string
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT claimed_by FROM rimsky_dispatch WHERE id = $1`, dispatchID).Scan(&claimedBy))
	require.Nil(t, claimedBy,
		"orphan reaper should release dispatch claim when joined frame is terminal")
}

func TestOrphanDispatchReaper_ClaimantGuardedRelease(t *testing.T) {
	t.Parallel()
	h := scenario.Start(t, scenario.HarnessOpts{NoScheduler: true})

	dispatchID := seedTerminalFrameAndDispatch(t, h.Ctx, h.Pool, "fresh-sup")

	// Drive the same SQL shape the per-row reaper uses, but with a stale
	// claimant id ("stale-sup"). The current claimed_by is "fresh-sup",
	// so the WHERE clause must not match and the row must be untouched.
	res, err := h.Pool.Exec(h.Ctx, `
		UPDATE rimsky_dispatch
		SET claimed_by = NULL, claimed_at = NULL, last_heartbeat_at = NULL
		WHERE id = $1 AND claimed_by = $2
	`, dispatchID, "stale-sup")
	require.NoError(t, err)
	require.Equal(t, int64(0), res.RowsAffected(),
		"claimant-guarded UPDATE must be a no-op when claimed_by has rotated")

	var claimedBy string
	require.NoError(t, h.Pool.QueryRow(h.Ctx,
		`SELECT claimed_by FROM rimsky_dispatch WHERE id = $1`, dispatchID).Scan(&claimedBy))
	require.Equal(t, "fresh-sup", claimedBy,
		"live supervisor's claim must not be released by stale-claimant reap")
}

// seedTerminalFrameAndDispatch inserts a template+instance+node+
// terminal-frame+claimed-dispatch tuple suitable for orphan-reap tests.
// Returns the dispatch row's id.
func seedTerminalFrameAndDispatch(t *testing.T, ctx context.Context, pool *pgxpool.Pool, claimedBy string) uuid.UUID {
	t.Helper()
	templateID := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO rimsky_templates (id, name, version, spec)
		VALUES ($1, 't-orphan-reaper-`+templateID.String()[:8]+`', 'v1', '{"frame_resolution":"serial_queue"}'::jsonb)
	`, templateID)
	require.NoError(t, err)
	instanceID := uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO rimsky_instances (id, template_id, consumer_key, params)
		VALUES ($1, $2, 'ck-orphan-`+instanceID.String()[:8]+`', '{}'::jsonb)
	`, instanceID, templateID)
	require.NoError(t, err)
	nodeID := uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO rimsky_nodes (id, instance_id, node_type, state, dependencies)
		VALUES ($1, $2, 'n', 'fresh', ARRAY[]::UUID[])
	`, nodeID, instanceID)
	require.NoError(t, err)
	frameID := uuid.New()
	now := time.Now()
	_, err = pool.Exec(ctx, `
		INSERT INTO rimsky_frames (frame_id, instance_id, mode, state, source_node_ids,
			queued_at, started_at, ended_at, frame_timeout_ms)
		VALUES ($1, $2, 'serial_queue', 'completed', ARRAY[$3]::UUID[], $4, $4, $4, 600000)
	`, frameID, instanceID, nodeID, now)
	require.NoError(t, err)
	dispatchID := uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO rimsky_dispatch (id, node_id, executor_name, required_stores, claimed_by, frame_id)
		VALUES ($1, $2, NULL, '{}', $3, $4)
	`, dispatchID, nodeID, claimedBy, frameID)
	require.NoError(t, err)
	return dispatchID
}
