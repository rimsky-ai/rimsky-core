// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package auth

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestActionMatches(t *testing.T) {
	cases := []struct {
		entry, req string
		want       bool
	}{
		{"*", "node:read", true},
		{"*", "", true},
		{"node:read", "node:read", true},
		{"node:read", "node:write", false},
		{"auth:*", "auth:create", true},
		{"auth:*", "auth:rotate", true},
		{"auth:*", "authority:create", false},
		{"auth:*", "auth:", true},
		{"*:read", "node:read", true},
		{"*:read", "instance:read", true},
		{"*:read", "node:readwrite", false},
		{"*:read", "node:read:extra", false},
		{"*:read", ":read", true},
		{"foo:bar", "foo:baz", false},
	}
	for _, c := range cases {
		got := ActionMatches(c.entry, c.req)
		require.Equal(t, c.want, got, "ActionMatches(%q, %q)", c.entry, c.req)
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
		require.NoError(t, ValidateActionString(s), "ValidateActionString(%q)", s)
	}
	bad := []string{
		"",
		"nocolon",
		"instance:*:thing",
		"ins*ance:read",
		"foo:bar:*",
	}
	for _, s := range bad {
		require.Error(t, ValidateActionString(s), "ValidateActionString(%q)", s)
	}
}
