// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Unit coverage for the concept:terminal-tag gate-1 literal extractor.

package node

import (
	"reflect"
	"testing"
)

func TestExtractPayloadTagLiterals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		when string
		want []string
	}{
		{"empty", "", nil},
		{"single double-quoted membership", `"loop" in payload.tags`, []string{"loop"}},
		{"single single-quoted membership", `'loop' in payload.tags`, []string{"loop"}},
		{"contains form", `payload.tags.contains("done")`, []string{"done"}},
		{"both forms in same predicate", `"loop" in payload.tags && payload.tags.contains("done")`, []string{"loop", "done"}},
		{"deduplicates repeats", `"loop" in payload.tags || "loop" in payload.tags`, []string{"loop"}},
		{"ignores variable operand", `tag in payload.tags`, nil},
		{"ignores unrelated CEL", `payload.error_class == "x"`, nil},
		{"multiple distinct tags", `"a" in payload.tags && "b" in payload.tags`, []string{"a", "b"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := extractPayloadTagLiterals(tc.when)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("extractPayloadTagLiterals(%q) = %v, want %v", tc.when, got, tc.want)
			}
		})
	}
}
