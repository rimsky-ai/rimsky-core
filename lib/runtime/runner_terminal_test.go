// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"testing"

	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
)

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

	seen := make(map[string]string, len(got))
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

func TestWaitSetTopicKindFor_MessageRetired(t *testing.T) {
	bucket := waitSetTopicKindFor(signalpkg.TypePath("message/invalidate/operator/n"))
	if bucket == "message" {
		t.Fatalf("waitSetTopicKindFor(message/...) = %q, but `message` topic_kind retired", bucket)
	}
}
