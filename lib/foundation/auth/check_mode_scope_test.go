// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package auth

import "testing"

func TestCheckGrant_ModeFloor(t *testing.T) {
	dryRun := Grant{{Action: "instance:create", Mode: ModeDryRun}}
	res := CheckGrant(dryRun, "instance:create", nil)
	if !res.Allowed {
		t.Fatalf("dry_run entry should allow: %+v", res)
	}
	if res.Mode != ModeDryRun {
		t.Fatalf("Mode = %q, want %q (pinned floor)", res.Mode, ModeDryRun)
	}

	explicit := Grant{{Action: "instance:create", Mode: ModeExecute}}
	if got := CheckGrant(explicit, "instance:create", nil).Mode; got != ModeExecute {
		t.Fatalf("explicit execute: Mode = %q, want %q", got, ModeExecute)
	}

	unset := Grant{{Action: "instance:create"}}
	if got := CheckGrant(unset, "instance:create", nil).Mode; got != ModeExecute {
		t.Fatalf("unset mode: Mode = %q, want default %q", got, ModeExecute)
	}

	denied := CheckGrant(dryRun, "instance:read", nil)
	if denied.Allowed {
		t.Fatalf("non-matching action should deny: %+v", denied)
	}
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
			if !res.Allowed {
				t.Fatalf("both entries match; expected Allowed=true: %+v", res)
			}
			if res.Mode != ModeExecute {
				t.Fatalf("Mode = %q, want %q (execute must beat dry_run regardless of order)", res.Mode, ModeExecute)
			}
		})
	}

	scopedMix := Grant{
		{Action: "template:deploy", Scope: map[string]string{"template_tag": "analytics"}, Mode: ModeDryRun},
		{Action: "template:deploy", Mode: ModeExecute},
	}
	res := CheckGrant(scopedMix, "template:deploy", map[string]string{"template_tag": "analytics"})
	if !res.Allowed {
		t.Fatalf("scoped+unscoped mix should allow: %+v", res)
	}
	if res.Mode != ModeExecute {
		t.Fatalf("scoped+unscoped mix Mode = %q, want %q (execute wins)", res.Mode, ModeExecute)
	}
}

func TestCheckGrant_ScopeMatch(t *testing.T) {
	g := Grant{{Action: "template:register", Scope: map[string]string{"template_tag": "analytics"}}}

	inScope := CheckGrant(g, "template:register", map[string]string{"template_tag": "analytics"})
	if !inScope.Allowed {
		t.Fatalf("in-scope target should allow: %+v", inScope)
	}

	outOfScope := CheckGrant(g, "template:register", map[string]string{"template_tag": "billing"})
	if outOfScope.Allowed {
		t.Fatalf("out-of-scope target must deny: %+v", outOfScope)
	}

	missingKey := CheckGrant(g, "template:register", map[string]string{"other": "analytics"})
	if missingKey.Allowed {
		t.Fatalf("target missing scoped key must deny: %+v", missingKey)
	}

	nilTarget := CheckGrant(g, "template:register", nil)
	if nilTarget.Allowed {
		t.Fatalf("nil target against a scoped entry must deny: %+v", nilTarget)
	}

	unscoped := Grant{{Action: "template:register"}}
	if !CheckGrant(unscoped, "template:register", map[string]string{"template_tag": "billing"}).Allowed {
		t.Fatalf("unscoped entry should allow any target")
	}
}
