// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package auth

import "testing"

func TestCheckGrantEmpty(t *testing.T) {
	res := CheckGrant(Grant{}, "node:read")
	if res.Allowed {
		t.Fatalf("empty grant must deny")
	}
	if res.MatchedIdx != -1 {
		t.Fatalf("MatchedIdx %d != -1", res.MatchedIdx)
	}
}

func TestCheckGrantWildcardStar(t *testing.T) {
	g := Grant{{Action: "*"}}
	res := CheckGrant(g, "instance:create")
	if !res.Allowed || res.Mode != ModeExecute {
		t.Fatalf("star grant should allow with execute: %+v", res)
	}
}

func TestCheckGrantVerbSuffix(t *testing.T) {
	g := Grant{{Action: "*:read"}}
	if !CheckGrant(g, "node:read").Allowed {
		t.Fatalf("*:read should allow node:read")
	}
	if CheckGrant(g, "node:write").Allowed {
		t.Fatalf("*:read should not allow node:write")
	}
}

func TestCheckGrantFirstMatchWins_SpecificFirst(t *testing.T) {
	g := Grant{
		{Action: "instance:create", Mode: ModeDryRun},
		{Action: "*"},
	}
	res := CheckGrant(g, "instance:create")
	if !res.Allowed || res.Mode != ModeDryRun {
		t.Fatalf("specific-first: expected dry_run; got %+v", res)
	}
	// A different action falls through to the wildcard.
	res = CheckGrant(g, "node:read")
	if !res.Allowed || res.Mode != ModeExecute {
		t.Fatalf("wildcard fallthrough: expected execute; got %+v", res)
	}
}

func TestCheckGrantFirstMatchWins_WildcardFirst(t *testing.T) {
	g := Grant{
		{Action: "*"},
		{Action: "instance:create", Mode: ModeDryRun},
	}
	res := CheckGrant(g, "instance:create")
	if !res.Allowed || res.Mode != ModeExecute {
		t.Fatalf("wildcard-first: expected execute; got %+v", res)
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
