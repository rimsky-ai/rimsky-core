// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Shared helpers for frame_resolution scenario tests. Direct DB queries
// against rimsky_frames, plus invalidate-firing utilities that bypass
// the controlapi when a test wants to drive many invalidates rapidly.
//
// Helpers run through the persistence driver (or the scenario harness's
// raw-SQL escape hatches) so this file stays out of the pgx-isolation
// depguard scope.
package frame_resolution

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	"github.com/fallguyconsulting/rimsky/foundation/shared"
	"github.com/fallguyconsulting/rimsky/graph/frame"
	"github.com/fallguyconsulting/rimsky/graph/scenario"
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

// listFrames returns rimsky_frames rows for the given instance, ordered
// by queued_at ascending.
func listFrames(t *testing.T, h *scenario.Harness, instanceID shared.UUID) []frameRow {
	t.Helper()
	var out []frameRow
	h.QuerySQL(`
		SELECT frame_id, instance_id, frame_resolution_mode, state, source_node_ids,
		       queued_at, started_at, ended_at, frame_timeout_ms
		FROM rimsky_frames
		WHERE instance_id = $1
		ORDER BY queued_at ASC
	`, []any{uuid.UUID(instanceID)}, func(scan func(...any) error) error {
		var r frameRow
		if err := scan(
			&r.FrameID, &r.InstanceID, &r.Mode, &r.State, &r.SourceNodeIDs,
			&r.QueuedAt, &r.StartedAt, &r.EndedAt, &r.FrameTimeoutMs,
		); err != nil {
			return err
		}
		out = append(out, r)
		return nil
	})
	return out
}

func countFramesByState(t *testing.T, h *scenario.Harness, instanceID shared.UUID, state string) int {
	t.Helper()
	var n int
	h.QueryRowSQL(`
		SELECT count(*) FROM rimsky_frames WHERE instance_id = $1 AND state = $2
	`, []any{uuid.UUID(instanceID), state}, &n)
	return n
}

// fireInvalidate runs frame.EnqueueOrCoalesce in its own short tx via
// the persistence driver. Used by tests that want to fire many
// invalidates rapidly without going through the controlapi HTTP path.
func fireInvalidate(t *testing.T, h *scenario.Harness, instanceID, sourceNodeID shared.UUID) shared.UUID {
	t.Helper()
	var fid uuid.UUID
	require.NoError(t, h.Driver.Tables().Transaction(h.Ctx, func(ctx context.Context, tx persistence.Tx) error {
		got, err := frame.EnqueueOrCoalesce(ctx, h.Driver.Tables(), tx,
			uuid.UUID(instanceID), uuid.UUID(sourceNodeID))
		if err != nil {
			return err
		}
		fid = got
		return nil
	}))
	return shared.UUID(fid)
}

func waitForFramesByState(t *testing.T, h *scenario.Harness, instanceID shared.UUID, state string, want int, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if countFramesByState(t, h, instanceID, state) == want {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}
