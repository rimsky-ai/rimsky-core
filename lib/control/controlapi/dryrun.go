// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: dry-run

package controlapi

import (
	"errors"
	"net/http"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
)

var errDryRunOK = errors.New("dry-run validation passed; mutation skipped")

func WriteDryRunResponse(w http.ResponseWriter, r *http.Request, intent string, details map[string]any) bool {
	if ModeFromContext(r.Context()) != auth.ModeDryRun {
		return false
	}
	WriteDryRunResponseForced(w, intent, details)
	return true
}

func WriteDryRunResponseForced(w http.ResponseWriter, intent string, details map[string]any) {
	out := map[string]any{"dry_run": true}
	out[intent] = details
	writeJSON(w, http.StatusOK, out)
}
