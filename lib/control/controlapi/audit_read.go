// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

var auditKinds = []string{
	auth.EventAccessAttempted,
	auth.EventAccessDenied,
	auth.EventKeyCreated,
	auth.EventKeyRevoked,
	auth.EventKeyRotated,
}

func registerAuditRoutes(r chi.Router, deps AppDeps) {
	r.Get("/audit", gate(deps, "audit:read", handleListAudit(deps)))
}

func handleListAudit(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
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
			filter.Kind = wire
		}
		if s := q.Get("key_id"); s != "" {
			filter.KeyID = &s
		}
		if s := q.Get("key_name"); s != "" {
			filter.KeyName = &s
		}
		if s := q.Get("action"); s != "" {
			filter.ActionExact = &s
		} else if s := q.Get("action_prefix"); s != "" {
			filter.ActionPrefix = &s
		}
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
