// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package frame_resolution

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

type frameRow struct {
	FrameID             uuid.UUID
	InstanceID          uuid.UUID
	State               string
	TriggeringMessageID uuid.UUID
	StartedAt           *time.Time
	EndedAt             *time.Time
}

func listFrames(t *testing.T, h *scenario.Harness, instanceID shared.UUID) []frameRow {
	t.Helper()
	var out []frameRow
	h.QuerySQL(`
		SELECT f.frame_id, f.instance_id,
		       CASE
		           WHEN f.ended_at IS NULL THEN 'running'
		           WHEN EXISTS (SELECT 1 FROM rimsky_node_runs r WHERE r.frame_id = f.frame_id AND r.state = 'failed') THEN 'failed'
		           ELSE 'completed'
		       END,
		       f.triggering_message_id, f.started_at, f.ended_at
		FROM rimsky_frames f
		WHERE f.instance_id = $1
		ORDER BY f.started_at ASC
	`, []any{uuid.UUID(instanceID)}, func(scan func(...any) error) error {
		var r frameRow
		if err := scan(
			&r.FrameID, &r.InstanceID, &r.State, &r.TriggeringMessageID,
			&r.StartedAt, &r.EndedAt,
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
		SELECT count(*) FROM rimsky_frames f
		 WHERE instance_id = $1
		   AND CASE
		         WHEN f.ended_at IS NULL THEN 'running'
		         WHEN EXISTS (SELECT 1 FROM rimsky_node_runs r WHERE r.frame_id = f.frame_id AND r.state = 'failed') THEN 'failed'
		         ELSE 'completed'
		       END = $2
	`, []any{uuid.UUID(instanceID), state}, &n)
	return n
}

// @decision: empty-message-as-root-trigger
// @story: empty-message-wakes-roots
func postInvalidateMessage(t *testing.T, h *scenario.Harness, instanceID shared.UUID) shared.UUID {
	t.Helper()
	idemKey := "post-invalidate-message-" + instanceID.String() + "-" + uuid.NewString()
	return h.PostInstanceMessage(instanceID, "", nil, idemKey)
}

func waitForFramesByState(t *testing.T, h *scenario.Harness, instanceID shared.UUID, state string, want int) {
	t.Helper()
	for countFramesByState(t, h, instanceID, state) != want {
		time.Sleep(50 * time.Millisecond)
	}
}

// @concept: run-scope
func createFreshRunScope(t *testing.T, h *scenario.Harness, instanceID shared.UUID) shared.UUID {
	t.Helper()
	var graphName string
	h.QueryRowSQL(
		`SELECT graph_name FROM rimsky_run_scopes WHERE instance_id = $1 AND parent_run_scope_id IS NULL LIMIT 1`,
		[]any{uuid.UUID(instanceID)}, &graphName,
	)
	scopeID := uuid.New()
	h.ExecSQL(
		`INSERT INTO rimsky_run_scopes (id, graph_name, instance_id) VALUES ($1, $2, $3)`,
		scopeID, graphName, uuid.UUID(instanceID),
	)
	return shared.UUID(scopeID)
}
