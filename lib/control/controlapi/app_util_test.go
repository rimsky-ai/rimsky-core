// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package controlapi

import (
	"net/http/httptest"
	"testing"
)

func TestParseLimit(t *testing.T) {
	cases := []struct {
		name  string
		query string
		dflt  int
		want  int
	}{
		{name: "empty-uses-default", query: "", dflt: 100, want: 100},
		{name: "non-numeric-uses-default", query: "limit=abc", dflt: 100, want: 100},
		{name: "zero-uses-default", query: "limit=0", dflt: 100, want: 100},
		{name: "negative-uses-default", query: "limit=-5", dflt: 100, want: 100},
		{name: "in-range-honored", query: "limit=50", dflt: 100, want: 50},
		{name: "at-max-honored", query: "limit=500", dflt: 100, want: 500},
		{name: "over-max-clamped", query: "limit=99999999", dflt: 100, want: parseLimitMax},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url := "/x"
			if tc.query != "" {
				url += "?" + tc.query
			}
			req := httptest.NewRequest("GET", url, nil)
			got := parseLimit(req, tc.dflt)
			if got != tc.want {
				t.Fatalf("parseLimit(%q, %d) = %d, want %d", tc.query, tc.dflt, got, tc.want)
			}
		})
	}
}
