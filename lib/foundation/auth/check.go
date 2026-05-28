// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package auth

import "fmt"

// CheckResult describes the outcome of CheckGrant.
type CheckResult struct {
	Allowed    bool
	Mode       Mode // ModeExecute if entry didn't set mode and the request matched
	MatchedIdx int  // index into the grant of the matching entry; -1 if not allowed
}

// CheckGrant runs the first-match-wins algorithm against the grant
// for the given requestAction. Returns Allowed=false when no entry
// matches.
//
// First-match-wins is by iteration order over the grant array.
// "Specific" entries should appear before "general" entries when an
// operator wants a specific mode override to apply.
func CheckGrant(grant Grant, requestAction string) CheckResult {
	for i, e := range grant {
		if ActionMatches(e.Action, requestAction) {
			mode := e.Mode
			if mode == "" {
				mode = ModeExecute
			}
			return CheckResult{Allowed: true, Mode: mode, MatchedIdx: i}
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
