// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"testing"

	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
)

// TestWaitSetTopicKindFor_FullTaxonomy pins waitSetTopicKindFor to the
// full 5-value signal taxonomy (terminal | transient | attribute | event
// | message), one bucket per top-level kind, with NO two distinct signal
// classes collapsed onto the same value.
//
// This is the RED proof for S-cascade-waitset-topic-taxonomy: today the
// mapper folds terminal/transient/message all onto the legacy "state"
// bucket, so the topic_kind ledger cannot tell a terminal-gated edge from
// a transient-gated one from a message-gated one. The wait-set ledger is
// supposed to record the actual signal class an edge gates on; a lossy
// 3-into-5 collapse defeats that. A later GREEN pass widens the mapper
// (and the DB CHECK) so each kind reads its own value.
//
// The assertion that guards against re-collapse is the no-two-classes-on-
// the-same-bucket check at the end: terminal, transient, and message must
// land on three distinct, kind-named values — not be silently re-merged
// onto a shared "state" bucket by a future refactor.
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
		{"message/invalidate/operator/n", signalpkg.TypePath("message/invalidate/operator/n"), "message"},
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
	// the legacy mapper folded terminal/transient/message all onto
	// "state", which is exactly the lossiness this taxonomy widening
	// exists to remove.
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
