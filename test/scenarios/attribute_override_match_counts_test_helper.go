// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import (
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/test/support/scenario"
)

// @concept: attribute
func attributeOverrideMatchCounts(t *testing.T, h *scenario.Harness, instanceID shared.UUID, size int) []int64 {
	t.Helper()
	var counts map[int64]int64
	if err := h.Persist.Transaction(context.Background(), func(ctx context.Context, tx persistence.Tx) error {
		var e error
		counts, e = h.Persist.Events().CountAttributeOverrideMatchesByIndex(ctx, instanceID, tx)
		return e
	}); err != nil {
		t.Fatalf("attributeOverrideMatchCounts: %v", err)
	}
	out := make([]int64, size)
	for idx, cnt := range counts {
		if idx < 0 || int(idx) >= size {
			t.Fatalf("attributeOverrideMatchCounts: match event override_index %d out of range [0,%d)", idx, size)
		}
		out[idx] = cnt
	}
	return out
}
