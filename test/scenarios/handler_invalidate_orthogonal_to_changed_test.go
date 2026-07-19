// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

func waitForEventCount(t *testing.T, h *scenario.Harness, nodeID shared.UUID, kind string, want int) {
	t.Helper()
	for {
		var count int
		_ = h.Pool.QueryRow(h.Ctx,
			`SELECT count(*) FROM rimsky_events WHERE node_id = $1 AND kind = $2`,
			nodeID, kind).Scan(&count)
		if count >= want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}
