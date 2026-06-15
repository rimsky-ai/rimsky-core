// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"testing"

	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
)

// TestWaitSetTopicKindFor_FullTaxonomy pins waitSetTopicKindFor to the
// 4-value signal taxonomy (terminal | transient | attribute | event),
// one bucket per top-level kind, with NO two distinct signal classes
// collapsed onto the same value.
//
// The 'message' bucket retired with the 2026-06-14 message-schema-layer
// reshape (Pass 4): the `message/*` top-level kind is gone from the
// canonical taxonomy. Message arrival is now a virtual-node settle
// whose subscribers wake via stale-marking, NOT via wait-set rows
// keyed on a virtual sender run — so no signal that flows through this
// mapper carries a `message/*` type-path.
//
// The assertion that guards against re-collapse is the no-two-classes-on-
// the-same-bucket check at the end: terminal, transient, attribute, and
// event must land on four distinct, kind-named values — not be silently
// re-merged onto a shared "state" bucket by a future refactor.
func TestWaitSetTopicKindFor_FullTaxonomy(t *testing.T) {
	cases := []struct {
		name    string
		pattern signalpkg.TypePath
		want    string
	}{
		{"terminal/success", signalpkg.TypePath("terminal/success"), "terminal"},
		{"transient/await_async", signalpkg.TypePath("transient/await_async"), "transient"},
		{"attribute/x/changed", signalpkg.TypePath("attribute/x/changed"), "attribute"},
		{"event/foo", signalpkg.TypePath("event/foo"), "event"},
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
