// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Request-target extraction for scope-matched permission checks. The
// permission gate composes an action string with a resource-selector
// `target` (e.g. {"template_tag": "analytics"}) and lets CheckGrant
// reject a request whose action matches a scoped grant entry but whose
// target falls outside the entry's Scope. This file owns the per-action
// rule that reads the request's scopeable dimension out of the captured
// JSON body so the matcher has something to match against.
//
// An action with no scopeable dimension returns an empty target. An
// empty target is satisfied only by unscoped grant entries (the
// empty-Scope rule in auth.ScopeMatches), which preserves the behavior
// of every unscoped grant in use today.
//
// @concept: permission

package controlapi

import (
	"encoding/json"
	"net/http"
)

// requestTarget builds the resource-selector map that auth.CheckGrant
// matches a scoped grant entry's Scope against, for the given action.
//
// The body is the already-captured request body (captureBody re-attaches
// the bytes to r.Body, so reading the JSON here does not consume the
// handler's view). r is passed for actions whose scopeable dimension
// lives in the URL or query string rather than the body; today only
// body-sourced selectors exist, but the signature keeps that seam open
// without a later breaking change.
//
// Returns an empty (non-nil) map for any action with no scopeable
// dimension. An empty target only satisfies unscoped grant entries, so an
// unrecognized or unscopeable action keeps today's unscoped-grant
// behavior rather than silently denying.
func requestTarget(action string, body []byte, r *http.Request) map[string]string {
	_ = r // URL/query-sourced selectors are not used yet; see doc comment.
	target := map[string]string{}
	switch action {
	case "template:register":
		// POST /templates body is `{spec: {...}, tag, source}`. The tag is
		// the scopeable dimension — a grant scoped to {template_tag: X}
		// must reject a register that tags the template anything but X.
		// An empty/absent tag leaves the target empty so an unscoped
		// register grant still matches (untagged registration is not a
		// scope violation; it simply has no template_tag to match).
		if tag := registerRequestTag(body); tag != "" {
			target["template_tag"] = tag
		}
	}
	return target
}

// registerRequestTag pulls the `tag` field out of a POST /templates body
// without rejecting on the body's other fields (the gate runs before the
// handler's strict decode, so it must tolerate extra/unknown keys). A
// malformed body yields an empty tag, which leaves the target empty —
// the strict handler decode downstream is what rejects a malformed body
// with a 400, so the gate must not double-reject here.
func registerRequestTag(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var probe struct {
		Tag string `json:"tag"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return ""
	}
	return probe.Tag
}
