// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package auth

import "fmt"

// CheckResult describes the outcome of CheckGrant.
type CheckResult struct {
	Allowed    bool
	MatchedIdx int // index into the grant of a matching entry; -1 if not allowed
}

// CheckGrant evaluates the grant for the given requestAction by set
// membership: the request is allowed iff any entry's action matches
// (wildcard grammar unchanged). Returns Allowed=false when no entry
// matches. Order is not significant — any match allows.
//
// Permission is binary (allow/deny only); the request mode (execute vs
// dry_run) is resolved from the request flag, not the grant.
func CheckGrant(grant Grant, requestAction string) CheckResult {
	for i, e := range grant {
		if ActionMatches(e.Action, requestAction) {
			return CheckResult{Allowed: true, MatchedIdx: i}
		}
	}
	return CheckResult{Allowed: false, MatchedIdx: -1}
}

// ValidateGrant runs ValidateActionString over every entry. Used at
// POST /auth/keys time to reject grants that don't pass the wildcard
// grammar. The "is this action registered with the server?" check
// happens separately via the per-process action registry in
// control/controlapi/.
func ValidateGrant(grant Grant) error {
	if len(grant) == 0 {
		return fmt.Errorf("grant is empty")
	}
	for i, e := range grant {
		if err := ValidateActionString(e.Action); err != nil {
			return fmt.Errorf("entry %d: %w", i, err)
		}
	}
	return nil
}
