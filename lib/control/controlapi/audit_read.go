// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: event-log
// @concept: permission

package controlapi

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

var auditKinds = []string{
	auth.EventAccessAttempted.String(),
	auth.EventAccessDenied.String(),
	auth.EventKeyCreated.String(),
	auth.EventKeyRevoked.String(),
	auth.EventKeyRotated.String(),
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
			if !slices.Contains(auditKinds, wire) {
				badRequest(w, "kind not in audit allowlist: "+wire+
					" (the /audit surface accepts only auth.* kinds; expected one of: "+
					strings.Join(auditKinds, ", ")+")")
				return
			}
			filter.KindIn = []string{wire}
		}
		if s := q.Get("key_id"); s != "" {
			filter.AuditPayload.KeyID = &s
		}
		if s := q.Get("key_name"); s != "" {
			filter.AuditPayload.KeyName = &s
		}
		if s := q.Get("action"); s != "" {
			filter.AuditPayload.ActionExact = &s
		} else if s := q.Get("action_prefix"); s != "" {
			filter.AuditPayload.ActionPrefix = &s
		}
		if s := q.Get("target"); s != "" {
			filter.AuditPayload.RequestPath = &s
		}
		if s := q.Get("status"); s != "" {
			n, err := strconv.Atoi(s)
			if err != nil {
				badRequest(w, "invalid status (integer required)")
				return
			}
			filter.AuditPayload.ResponseStatus = &n
		}
		if s := q.Get("mode"); s != "" {
			filter.AuditPayload.Mode = &s
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
				Payload:    e.Payload.Map(),
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
