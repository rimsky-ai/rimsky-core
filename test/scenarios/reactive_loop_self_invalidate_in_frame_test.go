// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package scenarios

import "testing"

func TestReactiveLoopSelfInvalidateInFrame(t *testing.T) {
	t.Skip("retired: self-invalidate emit retired; loop semantics shaped " +
		"by producer-side Unavailable + error_types: { \"acquire/unavailable\": [pass] } in the new model")
}
