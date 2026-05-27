// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Dry-run helpers. Handlers that honor the per-request Mode call
// `if WriteDryRunResponse(w, r, "would_have_X", details) { return }`
// after validation succeeds. The helper returns true iff a dry-run
// response was written and the caller should stop dispatch.
//
// See spec section "Dry-run mode".
//
// @concept: dry-run

package controlapi

import (
	"errors"
	"net/http"

	"github.com/rimsky-ai/rimsky-core/foundation/auth"
)

// authModeDryRun re-exports auth.ModeDryRun as a file-scope alias so
// handler files don't have to import the auth package just for the
// mode comparison.
const authModeDryRun = auth.ModeDryRun

// errDryRunOK is the sentinel handlers return from inside a
// transaction to signal "every validation step has succeeded; do not
// commit the mutation; the outer caller should write the synthetic
// dry-run envelope." Using a sentinel lets handlers keep the
// validation logic inside the FOR UPDATE block (so validation runs
// against the same locked state a real call would) while still
// returning the right HTTP shape.
//
// Handler usage pattern:
//
//	if isDryRun {
//	    return errDryRunOK
//	}
//	// real mutation ...
//
// Then outside the tx:
//
//	if isDryRun && errors.Is(err, errDryRunOK) {
//	    WriteDryRunResponseForced(w, "would_have_X", ...)
//	    return
//	}
//
// Placing the gate AFTER every validation step a real call would
// run is the contract: only the persistence/RPC step should be
// elided.
var errDryRunOK = errors.New("dry-run validation passed; mutation skipped")

// WriteDryRunResponse writes a 200 with `{ dry_run: true, <intent>:
// <details> }` and returns true iff the current request is dry-run.
// Returns false (and writes nothing) when the mode is execute, in
// which case the caller proceeds with the real mutation.
//
// `intent` is one of the spec's verbs ("would_have_created",
// "would_have_invalidated", etc.); `details` is the per-action shape
// (instance_id placeholder, template hash, etc.).
//
// Placement guideline: WriteDryRunResponse must be called AFTER every
// validation step a real call would run. Only the persistence /
// fan-out step should be elided. For handlers whose validation lives
// inside a transaction (`FOR UPDATE` locks, state-machine checks),
// return errDryRunOK from inside the tx and call
// WriteDryRunResponseForced from outside — see the helper docs above.
func WriteDryRunResponse(w http.ResponseWriter, r *http.Request, intent string, details map[string]any) bool {
	if ModeFromContext(r.Context()) != auth.ModeDryRun {
		return false
	}
	WriteDryRunResponseForced(w, intent, details)
	return true
}

// WriteDryRunResponseForced writes the synthetic dry-run envelope
// unconditionally. Used by handlers that need to gate dry-run inside
// a transaction (see errDryRunOK); the mode check has already been
// done by the handler.
func WriteDryRunResponseForced(w http.ResponseWriter, intent string, details map[string]any) {
	out := map[string]any{"dry_run": true}
	out[intent] = details
	writeJSON(w, http.StatusOK, out)
}
