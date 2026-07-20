// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package shared

import (
	"reflect"
	"testing"
)

func TestDedupStrings(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{name: "empty returns nil", in: nil, want: nil},
		{name: "no duplicates preserves order", in: []string{"a", "b", "c"}, want: []string{"a", "b", "c"}},
		{name: "duplicates collapse keeping first occurrence order", in: []string{"a", "b", "a", "c", "b"}, want: []string{"a", "b", "c"}},
		{name: "all duplicates collapse to one", in: []string{"x", "x", "x"}, want: []string{"x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DedupStrings(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("DedupStrings(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
