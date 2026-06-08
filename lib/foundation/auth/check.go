// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package auth

import "fmt"

// CheckResult describes the outcome of CheckGrant.
type CheckResult struct {
	Allowed    bool
	MatchedIdx int // index into the grant of a matching entry; -1 if not allowed

	// Mode is the matched entry's identity-bound write floor, defaulted
	// to ModeExecute when the entry pins no mode (and zero-valued when
	// the request is denied). The caller composes this with any
	// per-request dry-run flag — the effective mode is the stricter of
	// the two, so a grant pinned to ModeDryRun is never escalated to
	// execute by the request.
	Mode Mode
}

// CheckGrant evaluates the grant for the given requestAction and request
// resource target by set membership: the request is allowed iff any
// entry both action-matches (wildcard grammar unchanged) AND
// scope-matches the target (subset-satisfaction, see ScopeMatches).
// Order is not significant: the function scans ALL matching entries and
// picks the most-permissive Mode (ModeExecute beats ModeDryRun), so a
// key that holds both an `execute` entry and a `dry_run` entry for the
// same action always resolves to execute regardless of where the entries
// sit in the grant. MatchedIdx is the index of the entry whose Mode is
// reported (the first execute-mode match if any, else the first
// dry_run-mode match). Returns Allowed=false when no entry matches.
//
// target carries the request's resource selector (e.g.
// {"template_tag": "analytics"}) for scope evaluation. An entry with no
// scope is unscoped and matches any target, so a nil/empty target only
// satisfies unscoped entries.
func CheckGrant(grant Grant, requestAction string, target map[string]string) CheckResult {
	bestIdx := -1
	bestMode := Mode("")
	for i, e := range grant {
		if !ActionMatches(e.Action, requestAction) {
			continue
		}
		if !ScopeMatches(e.Scope, target) {
			continue
		}
		mode := e.Mode
		if mode == "" {
			mode = ModeExecute
		}
		if bestIdx == -1 {
			bestIdx = i
			bestMode = mode
			continue
		}
		// Execute beats dry_run regardless of iteration order: a key
		// with one execute entry and one dry_run entry on the same
		// action resolves to execute.
		if bestMode == ModeDryRun && mode == ModeExecute {
			bestIdx = i
			bestMode = mode
		}
	}
	if bestIdx == -1 {
		return CheckResult{Allowed: false, MatchedIdx: -1}
	}
	return CheckResult{Allowed: true, MatchedIdx: bestIdx, Mode: bestMode}
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
