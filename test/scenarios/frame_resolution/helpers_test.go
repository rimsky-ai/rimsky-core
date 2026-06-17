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
	QueuedAt            time.Time
	StartedAt           *time.Time
	EndedAt             *time.Time
	FrameTimeoutMs      int64
}

// listFrames returns rimsky_frames rows for the given instance, ordered
// by queued_at ascending.
func listFrames(t *testing.T, h *scenario.Harness, instanceID shared.UUID) []frameRow {
	t.Helper()
	var out []frameRow
	h.QuerySQL(`
		SELECT frame_id, instance_id, state, triggering_message_id,
		       queued_at, started_at, ended_at, frame_timeout_ms
		FROM rimsky_frames
		WHERE instance_id = $1
		ORDER BY queued_at ASC
	`, []any{uuid.UUID(instanceID)}, func(scan func(...any) error) error {
		var r frameRow
		if err := scan(
			&r.FrameID, &r.InstanceID, &r.State, &r.TriggeringMessageID,
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

// postInvalidateMessage posts an empty-message wake to the instance to
// drive a fresh-frame cycle through the real message-delivery /
// cascade machinery, and returns the message_id assigned by the
// control-api. Post-spec the runtime synthesizes no envelopes; the
// empty-message wake trigger is the universal way to wake every
// structural root via the same path operators use in production.
//
// Scope warning: the wake fires EVERY structural root in the template,
// not a named node. The frame_resolution tests use single-root
// templates, where "every structural root" is equivalent to "the one
// node". A multi-root template would see overreach; callers must own
// that constraint at the call site (no targetNodeID parameter exists
// for them to pinpoint a single node).
//
// Returns the message_id assigned by the control-api (NOT a frame_id
// — both are `shared.UUID`; readers conflating the two would silently
// misinterpret downstream lookups). The frame the message opens is
// found via `listFrames` filtered by `triggering_message_id`.
// @decision: empty-message-as-root-trigger
// @story: empty-message-wakes-roots
func postInvalidateMessage(t *testing.T, h *scenario.Harness, instanceID shared.UUID) shared.UUID {
	t.Helper()
	idemKey := "post-invalidate-message-" + instanceID.String() + "-" + uuid.NewString()
	return h.PostInstanceMessage(instanceID, "", nil, idemKey)
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
