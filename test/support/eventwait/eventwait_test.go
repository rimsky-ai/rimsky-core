// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package eventwait

import "testing"

func TestMatcherKindMatches(t *testing.T) {
	cases := []struct {
		name string
		m    Matcher
		kind string
		want bool
	}{
		{"empty matcher matches anything", Matcher{}, "work_started", true},
		{"exact kind match", Matcher{Kind: "work_started"}, "work_started", true},
		{"exact kind mismatch", Matcher{Kind: "work_started"}, "work_completed", false},
		{"prefix match", Matcher{KindPrefix: "terminal/"}, "terminal/success", true},
		{"prefix mismatch", Matcher{KindPrefix: "terminal/"}, "work_started", false},
		{"kind+prefix union: kind side matches", Matcher{Kind: "work_started", KindPrefix: "terminal/"}, "work_started", true},
		{"kind+prefix union: prefix side matches", Matcher{Kind: "work_started", KindPrefix: "terminal/"}, "terminal/error/timeout", true},
		{"kind+prefix union: neither matches", Matcher{Kind: "work_started", KindPrefix: "terminal/"}, "work_completed", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.m.kindMatches(c.kind); got != c.want {
				t.Errorf("kindMatches(%q) with matcher {Kind:%q KindPrefix:%q} = %v, want %v",
					c.kind, c.m.Kind, c.m.KindPrefix, got, c.want)
			}
		})
	}
}
