// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package auth

import "testing"

func TestActionMatches(t *testing.T) {
	cases := []struct {
		entry, req string
		want       bool
	}{
		{"*", "node:read", true},
		{"*", "", true}, // even empty matches "*"
		{"node:read", "node:read", true},
		{"node:read", "node:write", false},
		{"auth:*", "auth:create", true},
		{"auth:*", "auth:rotate", true},
		{"auth:*", "authority:create", false},
		{"auth:*", "auth:", true}, // edge: ":*" → prefix is "auth:"
		{"*:read", "node:read", true},
		{"*:read", "instance:read", true},
		{"*:read", "node:readwrite", false},
		{"*:read", "node:read:extra", false},
		{"*:read", ":read", true},
		{"foo:bar", "foo:baz", false},
	}
	for _, c := range cases {
		got := ActionMatches(c.entry, c.req)
		if got != c.want {
			t.Errorf("ActionMatches(%q, %q): got %v want %v", c.entry, c.req, got, c.want)
		}
	}
}

func TestValidateActionString(t *testing.T) {
	good := []string{
		"instance:create",
		"*",
		"instance:*",
		"*:read",
		"complex-noun:complex-verb",
	}
	for _, s := range good {
		if err := ValidateActionString(s); err != nil {
			t.Errorf("ValidateActionString(%q): unexpected err %v", s, err)
		}
	}
	bad := []string{
		"",
		"nocolon",
		"instance:*:thing",
		"ins*ance:read",
		"foo:bar:*",
		"*",
	}
	// Mark the lone "*" out (it's valid). Re-trim that.
	bad = bad[:len(bad)-1]
	for _, s := range bad {
		if err := ValidateActionString(s); err == nil {
			t.Errorf("ValidateActionString(%q): expected error, got nil", s)
		}
	}
}
