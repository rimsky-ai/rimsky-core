// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Request-target extraction for scope-matched permission checks. The
// permission gate composes an action string with a resource-selector
// `target` (e.g. {"template_tag": "analytics"}) and lets CheckGrant
// reject a request whose action matches a scoped grant entry but whose
// target falls outside the entry's Scope. This file owns the per-action
// rules that read the request's scopeable dimension out of the URL
// path / captured JSON body so the matcher has something to match
// against.
//
// An action with no scopeable dimension returns no targets. An empty
// targets list collapses to the single empty target (see callers); the
// empty target only satisfies unscoped grant entries (the empty-Scope
// rule in auth.ScopeMatches), which preserves the behavior of every
// unscoped grant in use today.
//
// Lifecycle coverage: a `template_tag` scope protects the full template
// lifecycle — register / deploy / undeploy / deregister / tag-set /
// tag-delete / instance-create — not just register. For routes whose
// URL or body names a template by HASH, this file resolves the hash to
// the tag list (via deps.Persist.TemplateTags) and yields one candidate
// target per tag. The gate is satisfied if ANY candidate target
// satisfies the matched grant entry's Scope. A hash with zero tags has
// no scopeable dimension and falls back to the empty target (so an
// unscoped grant still matches; a template_tag-scoped grant fails as
// it would for an untagged register today).
//
// @concept: permission

package controlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

// requestTargets returns the list of resource-selector candidate maps
// that auth.CheckGrant matches a scoped grant entry's Scope against for
// the given action. The gate is satisfied iff ANY candidate satisfies
// the matched entry's Scope; an empty candidate (`{}`) only satisfies
// unscoped entries (the empty-Scope rule in auth.ScopeMatches), so
// returning a list with the lone empty target preserves today's
// unscoped-grant behavior for actions without a scopeable dimension.
//
// Today's scopeable dimensions:
//   - `template_tag` — protects the full template lifecycle. For routes
//     whose URL or body names a template by tag, the tag drives the
//     selector directly. For routes that name a template by HASH, this
//     function resolves the hash to its tag list via persist (read-only
//     auto-committed query) and emits one candidate per tag. A
//     hash-form URL whose template carries zero tags has no scopeable
//     dimension and emits the lone empty target (an unscoped grant
//     still matches; a tag-scoped grant fails — same behavior as an
//     untagged register).
//
// The body is the already-captured request body (captureBody re-attaches
// the bytes to r.Body, so reading the JSON here does not consume the
// handler's view). r is passed for actions whose scopeable dimension
// lives in the URL or query string.
func requestTargets(ctx context.Context, persist persistence.Tables, action string, body []byte, r *http.Request) []map[string]string {
	switch action {
	case "template:register":
		// @constraint: POST /templates body is `{spec: {...}, tag, source}`. The tag
		// is the scopeable dimension — a grant scoped to
		// {template_tag: X} must reject a register that tags the
		// template anything but X. An empty/absent tag leaves the
		// target empty so an unscoped register grant still matches
		// (untagged registration is not a scope violation; it simply
		// has no template_tag to match).
		if tag := registerRequestTag(body); tag != "" {
			return []map[string]string{{"template_tag": tag}}
		}
	case "template:deploy", "template:undeploy", "template:deregister":
		// @constraint: POST/DELETE /templates/{id} where {id} is a tag OR a hash.
		// Tag-form: scope by that tag. Hash-form: enumerate the tags
		// pointing at the hash and yield one candidate per tag.
		return templateIDTargets(ctx, persist, chi.URLParam(r, "id"))
	case "tag:set", "tag:delete":
		// @constraint: PUT/DELETE /tags/{tag}. The URL segment is the tag directly.
		if tag := chi.URLParam(r, "tag"); tag != "" {
			return []map[string]string{{"template_tag": tag}}
		}
	case "instance:create":
		// @constraint: POST /instances body is `{template, instance_key, params, ...}`.
		// `template` is a tag OR hash; resolve to the candidate set the
		// same way as the lifecycle URLs above so a `template_tag`-scoped
		// grant protects the full template lifecycle, not just register.
		if tpl := instanceCreateTemplate(body); tpl != "" {
			return templateIDTargets(ctx, persist, tpl)
		}
	}
	// @constraint: no scopeable dimension for this action: emit the lone empty
	// target so unscoped grants still match (preserve today's behavior).
	return []map[string]string{{}}
}

// templateIDTargets resolves an `idOrTag` template reference (from a
// URL segment or instance-create body) to the set of `template_tag`
// candidates. A tag-form yields a single-entry candidate set. A
// hash-form looks up the tag list via persist; zero tags collapses to
// the empty target (unscoped) and one-or-more tags yield one candidate
// per tag. Read-only auto-commit; safe to call ahead of the handler's
// own tx.
func templateIDTargets(ctx context.Context, persist persistence.Tables, idOrTag string) []map[string]string {
	if idOrTag == "" {
		return []map[string]string{{}}
	}
	if !looksLikeHash(idOrTag) {
		// @constraint: tag form — single candidate.
		return []map[string]string{{"template_tag": idOrTag}}
	}
	if persist == nil {
		// @constraint: no persistence wired (route-only test harness). Fall back to
		// the empty target so unscoped grants still match; a tag-scoped
		// grant fails as it would for an unresolvable hash.
		return []map[string]string{{}}
	}
	var rows []persistence.TemplateTagRow
	err := persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		r, err := persist.TemplateTags().ListByTemplate(ctx, idOrTag, tx)
		rows = r
		return err
	})
	if err != nil || len(rows) == 0 {
		return []map[string]string{{}}
	}
	out := make([]map[string]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, map[string]string{"template_tag": r.Tag})
	}
	return out
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

// instanceCreateTemplate pulls the `template` field out of a POST
// /instances body without rejecting on extras (the gate runs before
// the handler's strict decode; tolerant probe). Trims whitespace
// because the handler does too — an operator's accidental leading/
// trailing space must not flip the scope check past the handler's
// canonical form.
func instanceCreateTemplate(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var probe struct {
		Template string `json:"template"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return ""
	}
	return strings.TrimSpace(probe.Template)
}
