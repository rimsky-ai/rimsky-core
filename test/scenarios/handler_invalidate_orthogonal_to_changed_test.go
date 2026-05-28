// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Task 42 — handler_invalidate_orthogonal_to_changed retired under the
// 2026-05-14 subscription-cascade resolution: send-side
// handler.invalidate emits retired. The orthogonal-to-changed shape is
// expressed receiver-side: a receiver that subscribes to
// `terminal/success` WITHOUT a `when:` predicate fires on every
// terminal regardless of `payload.changed`; a receiver that adds
// `when: payload.changed` fires only on content-bearing transitions.
// Selectivity is purely subscriber-driven (CEL `when:` on the signal
// envelope), so the legacy sender-side "orthogonal to changed"
// semantics no longer have a natural shape.
package scenarios

import (
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

func TestHandlerInvalidateOrthogonalToChanged(t *testing.T) {
	t.Skip("retired: send-side handler.invalidate emit retired; orthogonal-" +
		"to-changed semantics expressed via receiver-side subscription " +
		"with sender `resolve: always_propagate` under the new model")
}

// waitForEventCount polls rimsky_events for the count of (node_id, kind)
// rows. Returns true once the count meets or exceeds want.
func waitForEventCount(t *testing.T, h *scenario.Harness, nodeID shared.UUID, kind string, want int, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var count int
		_ = h.Pool.QueryRow(h.Ctx,
			`SELECT count(*) FROM rimsky_events WHERE node_id = $1 AND kind = $2`,
			nodeID, kind).Scan(&count)
		if count >= want {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}
