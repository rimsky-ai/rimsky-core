// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package auth

import "fmt"

type CheckResult struct {
	Allowed bool
	Mode    Mode
}

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
		if bestMode == ModeDryRun && mode == ModeExecute {
			bestIdx = i
			bestMode = mode
		}
	}
	if bestIdx == -1 {
		return CheckResult{Allowed: false}
	}
	return CheckResult{Allowed: true, Mode: bestMode}
}

func HasAnyGrant(grant Grant, requestAction string) bool {
	for _, e := range grant {
		if ActionMatches(e.Action, requestAction) {
			return true
		}
	}
	return false
}

func ValidateGrant(grant Grant) error {
	if len(grant) == 0 {
		return fmt.Errorf("grant is empty")
	}
	for i, e := range grant {
		if err := ValidateActionString(e.Action); err != nil {
			return fmt.Errorf("entry %d: %w", i, err)
		}
		switch e.Mode {
		case "", ModeExecute, ModeDryRun:
		default:
			return fmt.Errorf("entry %d: %w: mode %q must be \"\", %q, or %q", i, ErrInvalidGrant, e.Mode, ModeExecute, ModeDryRun)
		}
	}
	return nil
}
