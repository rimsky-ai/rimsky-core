// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package claimproducer

import "testing"

func TestErrScopesConflictUnsupportedFallbackEmptyNeverConflicts(t *testing.T) {
	cases := []struct {
		name string
		a, b []byte
		want bool
	}{
		{"both nil", nil, nil, false},
		{"both empty slice", []byte{}, []byte{}, false},
		{"a nil b non-empty", nil, []byte("x"), false},
		{"a non-empty b nil", []byte("x"), nil, false},
		{"identical non-empty", []byte("ab"), []byte("ab"), true},
		{"different length", []byte("ab"), []byte("abc"), false},
		{"different bytes", []byte("ab"), []byte("ac"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ErrScopesConflictUnsupportedFallback(tc.a, tc.b)
			if got != tc.want {
				t.Fatalf("ErrScopesConflictUnsupportedFallback(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
