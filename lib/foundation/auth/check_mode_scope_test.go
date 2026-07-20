// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package auth

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckGrant_ModeFloor(t *testing.T) {
	dryRun := Grant{{Action: "instance:create", Mode: ModeDryRun}}
	res := CheckGrant(dryRun, "instance:create", nil)
	require.True(t, res.Allowed, "dry_run entry should allow: %+v", res)
	require.Equal(t, ModeDryRun, res.Mode, "pinned floor")

	explicit := Grant{{Action: "instance:create", Mode: ModeExecute}}
	require.Equal(t, ModeExecute, CheckGrant(explicit, "instance:create", nil).Mode, "explicit execute")

	unset := Grant{{Action: "instance:create"}}
	require.Equal(t, ModeExecute, CheckGrant(unset, "instance:create", nil).Mode, "unset mode defaults")

	denied := CheckGrant(dryRun, "instance:read", nil)
	require.False(t, denied.Allowed, "non-matching action should deny: %+v", denied)
}

func TestCheckGrant_ExecuteBeatsDryRun_OrderIndependent(t *testing.T) {
	dryThenExec := Grant{
		{Action: "instance:create", Mode: ModeDryRun},
		{Action: "instance:create", Mode: ModeExecute},
	}
	execThenDry := Grant{
		{Action: "instance:create", Mode: ModeExecute},
		{Action: "instance:create", Mode: ModeDryRun},
	}
	for _, tc := range []struct {
		name  string
		grant Grant
	}{
		{"dry-run-listed-first", dryThenExec},
		{"execute-listed-first", execThenDry},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := CheckGrant(tc.grant, "instance:create", nil)
			require.True(t, res.Allowed, "both entries match; expected Allowed=true: %+v", res)
			require.Equal(t, ModeExecute, res.Mode, "execute must beat dry_run regardless of order")
		})
	}

	scopedMix := Grant{
		{Action: "template:deploy", Scope: map[string]string{"template_tag": "analytics"}, Mode: ModeDryRun},
		{Action: "template:deploy", Mode: ModeExecute},
	}
	res := CheckGrant(scopedMix, "template:deploy", map[string]string{"template_tag": "analytics"})
	require.True(t, res.Allowed, "scoped+unscoped mix should allow: %+v", res)
	require.Equal(t, ModeExecute, res.Mode, "scoped+unscoped mix: execute wins")
}

func TestCheckGrant_ScopeMatch(t *testing.T) {
	g := Grant{{Action: "template:register", Scope: map[string]string{"template_tag": "analytics"}}}

	inScope := CheckGrant(g, "template:register", map[string]string{"template_tag": "analytics"})
	require.True(t, inScope.Allowed, "in-scope target should allow: %+v", inScope)

	outOfScope := CheckGrant(g, "template:register", map[string]string{"template_tag": "billing"})
	require.False(t, outOfScope.Allowed, "out-of-scope target must deny: %+v", outOfScope)

	missingKey := CheckGrant(g, "template:register", map[string]string{"other": "analytics"})
	require.False(t, missingKey.Allowed, "target missing scoped key must deny: %+v", missingKey)

	nilTarget := CheckGrant(g, "template:register", nil)
	require.False(t, nilTarget.Allowed, "nil target against a scoped entry must deny: %+v", nilTarget)

	unscoped := Grant{{Action: "template:register"}}
	require.True(t, CheckGrant(unscoped, "template:register", map[string]string{"template_tag": "billing"}).Allowed,
		"unscoped entry should allow any target")
}
