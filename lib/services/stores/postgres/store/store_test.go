// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package store

import (
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/action"
)

// TestValidIdent covers the construction-time validation of operator-
// supplied items_table names. The store uses fmt.Sprintf to
// interpolate the table name into SQL; rejecting non-identifier inputs
// at construction is the only thing standing between operator typo and
// SQL injection.
func TestValidIdent(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"items", true},
		{"my_items", true},
		{"items_42", true},
		{"a", true},
		{"_underscore_lead", true},
		// Empty / non-identifier forms.
		{"", false},
		{"42items", false}, // leading digit
		{"Items", false},   // uppercase
		{"my-table", false},
		{"my.table", false},
		{"my table", false},
		{"items;DROP TABLE", false},
		{"items'--", false},
	}
	for _, tc := range cases {
		t.Run(tc.s, func(t *testing.T) {
			if got := validIdent(tc.s); got != tc.want {
				t.Fatalf("validIdent(%q) = %v, want %v", tc.s, got, tc.want)
			}
		})
	}
}

// TestValidPickAction covers the pick-action vocabulary check —
// pg-store supports pop and recycle only.
func TestValidPickAction(t *testing.T) {
	good := []action.Kind{action.Pop, action.Recycle}
	for _, a := range good {
		if !validPickAction(a) {
			t.Errorf("validPickAction(%q) = false, want true", a)
		}
	}
	bad := []action.Kind{
		action.Kind(""),
		action.PopAndMove,
		action.PopAndDelete,
		action.Kind("delete"),
		action.Kind("release_to_back"),
		action.Kind("release_to_head"),
	}
	for _, a := range bad {
		if validPickAction(a) {
			t.Errorf("validPickAction(%q) = true, want false", a)
		}
	}
}

// TestNewRejectsEmptyConnection covers the early-fail at construction
// when the operator forgets the connection string. Doesn't need a real
// pool because the check happens before pgxpool.New.
func TestNewRejectsEmptyConnection(t *testing.T) {
	if _, err := New(t.Context(), Config{}); err == nil {
		t.Fatal("New with empty Connection should error; got nil")
	}
}
