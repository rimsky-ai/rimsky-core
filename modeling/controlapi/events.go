// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// events.go — GET /events. Paginated read of the append-only event log.
// Filterable by instance_id, node_id, kind, since, until.
package controlapi

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/modeling/shared"
)

type eventResponseItem struct {
	ID         int64          `json:"id"`
	InstanceID string         `json:"instance_id,omitempty"`
	NodeID     string         `json:"node_id,omitempty"`
	Kind       string         `json:"kind"`
	Payload    map[string]any `json:"payload"`
	OccurredAt time.Time      `json:"occurred_at"`
}

// registerEventsRoutes wires the /events group.
func registerEventsRoutes(r chi.Router, deps AppDeps) {
	r.Get("/events", handleListEvents(deps))
}

func handleListEvents(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		filter := persistence.EventListFilter{
			Kind: q.Get("kind"),
		}
		if s := q.Get("instance_id"); s != "" {
			id, err := uuid.Parse(s)
			if err != nil {
				badRequest(w, "invalid instance_id")
				return
			}
			u := shared.UUID(id)
			filter.InstanceID = &u
		}
		if s := q.Get("node_id"); s != "" {
			id, err := uuid.Parse(s)
			if err != nil {
				badRequest(w, "invalid node_id")
				return
			}
			u := shared.UUID(id)
			filter.NodeID = &u
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
		page, err := deps.Persist.Events().List(req.Context(), filter, pag, nil)
		if err != nil {
			writeError(w, err)
			return
		}
		out := make([]eventResponseItem, 0, len(page.Events))
		for _, e := range page.Events {
			item := eventResponseItem{
				ID:         e.ID,
				Kind:       e.Kind,
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
			"events":      out,
			"next_cursor": page.NextCursor,
		})
	}
}
