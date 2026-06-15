// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// audit_read.go — GET /audit. A filterable read over the auth audit
// slice of the event log: the `auth.*`-kind rows in rimsky_events.
// Distinct from GET /events (event:read) because audit data — actor
// identity, IP, user-agent, actions — is sensitive enough to gate
// separately, so this route is gated by the audit:read action.
//
// Filters: actor (key_id / key_name), action (exact or prefix), target
// (request_path), time range (since / until), result (status), mode,
// plus opaque cursor pagination — consistent with GET /events. The
// payload filters live in persistence.EventListFilter (backed by the
// auth-payload expression indexes); a nil filter pointer is a no-op so
// composing several filters never accidentally excludes a row.
//
// @concept: event-log
// @concept: permission

package controlapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

// auditKinds is the auth.* slice of the event log surfaced by GET
// /audit: the access rows (attempted / denied) plus the key-lifecycle
// rows (created / revoked / rotated).
//
// Filter contract over these heterogeneous payloads:
//   - Actor filters (key_id, key_name) span ALL auth rows — every kind
//     carries them (a rotation under its new key id; see
//     auth.KeyRotatedPayload). So ?key_id= is a complete actor query.
//   - Access filters (action, target, status, mode) exist only on the
//     access rows, so supplying one implicitly scopes the result to
//     access_attempted / access_denied — the key-lifecycle rows lack
//     those fields and fall out. That narrowing is intended (a rotation
//     has no response_status), not a silent drop.
var auditKinds = []string{
	auth.EventAccessAttempted,
	auth.EventAccessDenied,
	auth.EventKeyCreated,
	auth.EventKeyRevoked,
	auth.EventKeyRotated,
}

// registerAuditRoutes wires the /audit group.
func registerAuditRoutes(r chi.Router, deps AppDeps) {
	r.Get("/audit", gate(deps, "audit:read", handleListAudit(deps)))
}

func handleListAudit(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		// @constraint: validate ?kind= against both the proto enum AND the audit
		// surface's allowlist (auth.*). The intersection is the
		// rule: a kind that's valid in the proto enum but not in
		// the audit surface (e.g. state_transition) returns 400 —
		// the audit reader exists to surface auth audit data, not
		// arbitrary operational events. An unknown kind also
		// returns 400. Empty kind = the full auth.* set (today's
		// behavior).
		filter := persistence.EventListFilter{
			KindIn: auditKinds,
		}
		if s := q.Get("kind"); s != "" {
			parsed, err := events.ParseKindString(s)
			if err != nil {
				badRequest(w, "invalid kind: "+s+
					" (expected one of: "+
					strings.Join(auditKinds, ", ")+")")
				return
			}
			wire := parsed.String()
			if !strings.HasPrefix(wire, "auth.") {
				badRequest(w, "kind not in audit allowlist: "+wire+
					" (the /audit surface accepts only auth.* kinds; expected one of: "+
					strings.Join(auditKinds, ", ")+")")
				return
			}
			// @constraint: narrow the read to the single requested kind via
			// the exact-match Kind field; KindIn stays set so the
			// allowlist still gates downstream filters that may
			// fall through (the persistence layer AND-composes
			// Kind and KindIn). Both filtering on `kind = X` AND
			// `kind IN (auth.*)` is equivalent to just `kind = X`
			// when X is in the allowlist.
			filter.Kind = wire
		}
		if s := q.Get("key_id"); s != "" {
			filter.KeyID = &s
		}
		if s := q.Get("key_name"); s != "" {
			filter.KeyName = &s
		}
		// @constraint: action: exact takes precedence over prefix when both are given.
		if s := q.Get("action"); s != "" {
			filter.ActionExact = &s
		} else if s := q.Get("action_prefix"); s != "" {
			filter.ActionPrefix = &s
		}
		// @deliberate: target = the request path recorded in the audit
		// payload. Backed by the request_path expression index (partial,
		// scoped to kind LIKE 'auth.%') so the narrow stays
		// index-bounded.
		if s := q.Get("target"); s != "" {
			filter.RequestPath = &s
		}
		if s := q.Get("status"); s != "" {
			n, err := strconv.Atoi(s)
			if err != nil {
				badRequest(w, "invalid status (integer required)")
				return
			}
			filter.ResponseStatus = &n
		}
		// @constraint: mode filter (execute | dry_run).
		if s := q.Get("mode"); s != "" {
			filter.Mode = &s
		}
		if s := q.Get("since"); s != "" {
			t, err := time.Parse(time.RFC3339, s)
			if err != nil {
				badRequest(w, "invalid since (RFC3339 required)")
				return
			}
			filter.Since = &t
		}
		if s := q.Get("until"); s != "" {
			t, err := time.Parse(time.RFC3339, s)
			if err != nil {
				badRequest(w, "invalid until (RFC3339 required)")
				return
			}
			filter.Until = &t
		}
		pag := persistence.ListPagination{
			Limit:  parseLimit(req, 100),
			Cursor: q.Get("cursor"),
		}
		// @constraint: short fresh transaction per the cascade-graph read
		// discipline (mirror handleListEvents) — open a read tx, run the
		// single List, commit. No mutation rides this path.
		var page persistence.EventListResult
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			p, err := deps.Persist.Events().List(ctx, filter, pag, tx)
			page = p
			return err
		}); err != nil {
			writeError(w, err)
			return
		}
		out := make([]eventResponseItem, 0, len(page.Events))
		for _, e := range page.Events {
			item := eventResponseItem{
				ID:         e.ID,
				Kind:       e.KindRaw,
				Payload:    e.Payload,
				OccurredAt: e.OccurredAt,
			}
			if e.InstanceID != nil {
				item.InstanceID = e.InstanceID.String()
			}
			if e.NodeID != nil {
				item.NodeID = e.NodeID.String()
			}
			out = append(out, item)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"audit":       out,
			"next_cursor": page.NextCursor,
		})
	}
}
