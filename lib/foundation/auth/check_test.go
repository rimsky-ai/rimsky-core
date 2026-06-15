// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package auth

import "testing"

func TestCheckGrantEmpty(t *testing.T) {
	res := CheckGrant(Grant{}, "node:read", nil)
	if res.Allowed {
		t.Fatalf("empty grant must deny")
	}
	if res.MatchedIdx != -1 {
		t.Fatalf("MatchedIdx %d != -1", res.MatchedIdx)
	}
}

func TestCheckGrantWildcardStar(t *testing.T) {
	g := Grant{{Action: "*"}}
	res := CheckGrant(g, "instance:create", nil)
	if !res.Allowed {
		t.Fatalf("star grant should allow: %+v", res)
	}
}

func TestCheckGrantVerbSuffix(t *testing.T) {
	g := Grant{{Action: "*:read"}}
	if !CheckGrant(g, "node:read", nil).Allowed {
		t.Fatalf("*:read should allow node:read")
	}
	if CheckGrant(g, "node:write", nil).Allowed {
		t.Fatalf("*:read should not allow node:write")
	}
}

// TestCheckGrantSetMembership_AnyMatchAllows: evaluation is set
// membership — any entry whose action matches allows; order is not
// significant. A non-matching action is denied regardless of position.
func TestCheckGrantSetMembership_AnyMatchAllows(t *testing.T) {
	g := Grant{
		{Action: "instance:create"},
		{Action: "*:read"},
	}
	if !CheckGrant(g, "instance:create", nil).Allowed {
		t.Fatalf("specific entry should allow instance:create")
	}
	if !CheckGrant(g, "node:read", nil).Allowed {
		t.Fatalf("wildcard entry should allow node:read")
	}
	if CheckGrant(g, "node:reset", nil).Allowed {
		t.Fatalf("no entry matches node:reset; must deny")
	}
}

// TestCheckGrantSetMembership_OrderIrrelevant: the same grant in either
// order yields the same allow/deny decision (no first-match-wins).
func TestCheckGrantSetMembership_OrderIrrelevant(t *testing.T) {
	specificFirst := Grant{{Action: "instance:create"}, {Action: "*"}}
	wildcardFirst := Grant{{Action: "*"}, {Action: "instance:create"}}
	for _, action := range []string{"instance:create", "node:read"} {
		if CheckGrant(specificFirst, action, nil).Allowed != CheckGrant(wildcardFirst, action, nil).Allowed {
			t.Fatalf("order changed the decision for %q", action)
		}
		if !CheckGrant(specificFirst, action, nil).Allowed {
			t.Fatalf("wildcard grant should allow %q", action)
		}
	}
}

func TestValidateGrant(t *testing.T) {
	if err := ValidateGrant(nil); err == nil {
		t.Fatalf("nil grant should error")
	}
	if err := ValidateGrant(Grant{}); err == nil {
		t.Fatalf("empty grant should error")
	}
	if err := ValidateGrant(Grant{{Action: "instance:create"}}); err != nil {
		t.Fatalf("valid grant: %v", err)
	}
	if err := ValidateGrant(Grant{{Action: "instance:*"}, {Action: "nocolon"}}); err == nil {
		t.Fatalf("bad-action grant should error")
	}
}
