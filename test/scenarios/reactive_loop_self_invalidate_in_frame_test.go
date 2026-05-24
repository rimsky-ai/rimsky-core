// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Task 25 retired under the 2026-05-14 subscription-cascade resolution.
// Self-invalidate emits retire; receivers declare cascade coupling via
// subscriptions. The "loop until queue drains" shape is now expressed
// by the producer-side claim contract (Open returns Unavailable when
// the queue is empty; error_types: { "acquire/unavailable": [pass] }
// settles the dispatch fresh) without a self-referential cascade edge.
package scenarios

import "testing"

func TestReactiveLoopSelfInvalidateInFrame(t *testing.T) {
	t.Skip("retired: self-invalidate emit retired; loop semantics shaped " +
		"by producer-side Unavailable + error_types: { \"acquire/unavailable\": [pass] } in the new model")
}
