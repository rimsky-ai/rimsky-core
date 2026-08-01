// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package auth

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckGrantEmpty(t *testing.T) {
	res := CheckGrant(Grant{}, "node:read", nil)
	require.False(t, res.Allowed, "empty grant must deny")
}

func TestCheckGrantWildcardStar(t *testing.T) {
	g := Grant{{Action: "*"}}
	res := CheckGrant(g, "instance:create", nil)
	require.True(t, res.Allowed, "star grant should allow: %+v", res)
}

func TestCheckGrantVerbSuffix(t *testing.T) {
	g := Grant{{Action: "*:read"}}
	require.True(t, CheckGrant(g, "node:read", nil).Allowed, "*:read should allow node:read")
	require.False(t, CheckGrant(g, "node:write", nil).Allowed, "*:read should not allow node:write")
}

func TestCheckGrantSetMembership_AnyMatchAllows(t *testing.T) {
	g := Grant{
		{Action: "instance:create"},
		{Action: "*:read"},
	}
	require.True(t, CheckGrant(g, "instance:create", nil).Allowed, "specific entry should allow instance:create")
	require.True(t, CheckGrant(g, "node:read", nil).Allowed, "wildcard entry should allow node:read")
	require.False(t, CheckGrant(g, "node:reset", nil).Allowed, "no entry matches node:reset; must deny")
}

func TestCheckGrantSetMembership_OrderIrrelevant(t *testing.T) {
	specificFirst := Grant{{Action: "instance:create"}, {Action: "*"}}
	wildcardFirst := Grant{{Action: "*"}, {Action: "instance:create"}}
	for _, action := range []string{"instance:create", "node:read"} {
		require.Equal(t, CheckGrant(wildcardFirst, action, nil).Allowed, CheckGrant(specificFirst, action, nil).Allowed,
			"order changed the decision for %q", action)
		require.True(t, CheckGrant(specificFirst, action, nil).Allowed, "wildcard grant should allow %q", action)
	}
}

func TestValidateGrant(t *testing.T) {
	require.Error(t, ValidateGrant(nil), "nil grant should error")
	require.Error(t, ValidateGrant(Grant{}), "empty grant should error")
	require.NoError(t, ValidateGrant(Grant{{Action: "instance:create"}}), "valid grant")
	require.Error(t, ValidateGrant(Grant{{Action: "instance:*"}, {Action: "nocolon"}}), "bad-action grant should error")
	require.NoError(t, ValidateGrant(Grant{{Action: "instance:create", Mode: ModeExecute}}), "valid mode grant")
	require.Error(t, ValidateGrant(Grant{{Action: "instance:create", Mode: Mode("bogus")}}), "out-of-enum mode should error")
}
