// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package auth

import "testing"

// TestCheckGrant_ModeFloor: a matched entry's identity-bound mode floor
// is reported on CheckResult.Mode. An entry pinned to `dry_run` yields
// ModeDryRun; an `execute`-or-unset entry defaults to ModeExecute.
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

	// A denied request reports no allow and the zero Mode.
	denied := CheckGrant(dryRun, "instance:read", nil)
	if denied.Allowed {
		t.Fatalf("non-matching action should deny: %+v", denied)
	}
}

// TestCheckGrant_ScopeMatch: a scoped entry is honored by the matcher —
// it allows an in-scope target, denies an out-of-scope target, and
// denies a target missing the scoped key entirely.
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

	// An unscoped entry for the same action matches any target,
	// including extra keys, so least-privilege is opt-in per entry.
	unscoped := Grant{{Action: "template:register"}}
	if !CheckGrant(unscoped, "template:register", map[string]string{"template_tag": "billing"}).Allowed {
		t.Fatalf("unscoped entry should allow any target")
	}
}
