// Shared helpers for frame_resolution scenario tests. Direct DB queries
// against rimsky_frames, plus invalidate-firing utilities that bypass
// the controlapi when a test wants to drive many invalidates rapidly.
package frame_resolution

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/fallguy/rimsky/core/frame"
	"github.com/fallguy/rimsky/core/scenario"
	"github.com/fallguy/rimsky/core/shared"
)

type frameRow struct {
	FrameID        uuid.UUID
	InstanceID     uuid.UUID
	Mode           string
	State          string
	SourceNodeIDs  []uuid.UUID
	QueuedAt       time.Time
	StartedAt      *time.Time
	EndedAt        *time.Time
	FrameTimeoutMs int64
}

func listFrames(t *testing.T, pool *pgxpool.Pool, instanceID shared.UUID) []frameRow {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT frame_id, instance_id, mode, state, source_node_ids,
		       queued_at, started_at, ended_at, frame_timeout_ms
		FROM rimsky_frames
		WHERE instance_id = $1
		ORDER BY queued_at ASC
	`, uuid.UUID(instanceID))
	require.NoError(t, err)
	defer rows.Close()
	var out []frameRow
	for rows.Next() {
		var r frameRow
		require.NoError(t, rows.Scan(
			&r.FrameID, &r.InstanceID, &r.Mode, &r.State, &r.SourceNodeIDs,
			&r.QueuedAt, &r.StartedAt, &r.EndedAt, &r.FrameTimeoutMs,
		))
		out = append(out, r)
	}
	require.NoError(t, rows.Err())
	return out
}

func countFramesByState(t *testing.T, pool *pgxpool.Pool, instanceID shared.UUID, state string) int {
	t.Helper()
	var n int
	err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM rimsky_frames WHERE instance_id = $1 AND state = $2
	`, uuid.UUID(instanceID), state).Scan(&n)
	require.NoError(t, err)
	return n
}

// fireInvalidate runs frame.EnqueueOrCoalesce in its own short tx
// against the harness pool. Used by tests that want to fire many
// invalidates rapidly without going through the controlapi HTTP path.
func fireInvalidate(t *testing.T, h *scenario.Harness, instanceID, sourceNodeID shared.UUID) shared.UUID {
	t.Helper()
	tx, err := h.Pool.Begin(h.Ctx)
	require.NoError(t, err)
	defer tx.Rollback(h.Ctx) //nolint:errcheck

	fid, err := frame.EnqueueOrCoalesce(h.Ctx, tx, uuid.UUID(instanceID), uuid.UUID(sourceNodeID))
	require.NoError(t, err)
	require.NoError(t, tx.Commit(h.Ctx))
	return shared.UUID(fid)
}

func waitForFramesByState(t *testing.T, pool *pgxpool.Pool, instanceID shared.UUID, state string, want int, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if countFramesByState(t, pool, instanceID, state) == want {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func waitForFrameTerminal(t *testing.T, pool *pgxpool.Pool, frameID uuid.UUID, timeout time.Duration) (string, bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var state string
		err := pool.QueryRow(context.Background(), `SELECT state FROM rimsky_frames WHERE frame_id = $1`, frameID).Scan(&state)
		require.NoError(t, err)
		if state == "completed" || state == "failed" {
			return state, true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return "", false
}
