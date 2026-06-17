// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

// @blessed-invariant: terminal-atomic-commit — coverage anchor: see
//   test/scenarios/loop_counter_cap_e2e_test.go (runtime-side proof
//   that the settling verdict + attributes_delta + tags commit
//   together in one tx).
//
// @blessed-invariant: callback-determinism — coverage anchor: see
//   test/scenarios/agentic_executor_async_handoff_test.go (runtime-
//   side proof that the phase-check read and the terminal state
//   mutation share one tx on the callback path).
//
// @blessed-invariant: affirm-node-run-row — coverage anchor: see
//   test/scenarios/subscription_cascade_test.go (cascade walker is the
//   lazy-allocation primitive for the receiver's in-flight row; the
//   subsequent read returns the row id under the same tx).

import (
	"testing"

	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
)

// TestWaitSetTopicKindFor_FullTaxonomy pins waitSetTopicKindFor to
// the 3-value signal taxonomy (terminal | transient | attribute),
// one bucket per top-level kind, with NO two distinct signal classes
// collapsed onto the same value.
//
// @deliberate: the `event` bucket retired alongside `event/<name>` per
// TD-collapse-named-event-to-tags — observable non-terminal
// transitions ride as tags on the settling terminal verdict, not as
// their own wait-set discriminator. The `message` bucket retired
// earlier (2026-06-14 message-schema-layer reshape) for the same
// virtual-node-settle reason.
//
// The assertion that guards against re-collapse is the
// no-two-classes-on-the-same-bucket check at the end: terminal,
// transient, and attribute must land on three distinct, kind-named
// values — not be silently re-merged onto a shared "state" bucket by
// a future refactor.
func TestWaitSetTopicKindFor_FullTaxonomy(t *testing.T) {
	cases := []struct {
		name    string
		pattern signalpkg.TypePath
		want    string
	}{
		{"terminal/success", signalpkg.TypePath("terminal/success"), "terminal"},
		{"transient/await_async", signalpkg.TypePath("transient/await_async"), "transient"},
		{"attribute/x/changed", signalpkg.TypePath("attribute/x/changed"), "attribute"},
	}

	got := make(map[string]string, len(cases))
	for _, tc := range cases {
		bucket := waitSetTopicKindFor(tc.pattern)
		if bucket != tc.want {
			t.Errorf("waitSetTopicKindFor(%q) = %q, want %q "+
				"(each top-level signal kind must map to its own taxonomy value, "+
				"not a collapsed bucket)", tc.pattern, bucket, tc.want)
		}
		got[tc.name] = bucket
	}

	// @deliberate: No two DISTINCT signal classes may collapse onto the same bucket —
	// the legacy mapper folded terminal/transient onto "state", which is
	// exactly the lossiness this taxonomy widening exists to remove.
	seen := make(map[string]string, len(got)) // bucket → first class that claimed it
	for class, bucket := range got {
		if prior, dup := seen[bucket]; dup {
			t.Errorf("signal classes %q and %q both map to topic_kind %q; "+
				"distinct signal classes must not collapse onto the same wait-set bucket",
				prior, class, bucket)
			continue
		}
		seen[bucket] = class
	}
}

// TestWaitSetTopicKindFor_MessageRetired pins the 2026-06-14
// retirement of the `message/*` top-level kind from the wait-set
// topic_kind mapper. A type-path with a `message/` prefix is no longer
// a canonical signal — TopLevel() returns the empty kind for it, so
// the mapper falls through to the `state` defensive fallback. No
// wait-set row should ever carry a `message` topic_kind under the
// virtual-node-settle model (subscribers stale-mark; they do not
// wait-set-gate on a virtual sender run that doesn't exist).
func TestWaitSetTopicKindFor_MessageRetired(t *testing.T) {
	bucket := waitSetTopicKindFor(signalpkg.TypePath("message/invalidate/operator/n"))
	if bucket == "message" {
		t.Fatalf("waitSetTopicKindFor(message/...) = %q, but `message` topic_kind retired", bucket)
	}
}
