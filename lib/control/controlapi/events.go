// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package controlapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type eventResponseItem struct {
	ID         int64          `json:"id"`
	InstanceID string         `json:"instance_id,omitempty"`
	NodeID     string         `json:"node_id,omitempty"`
	Kind       string         `json:"kind"`
	Payload    map[string]any `json:"payload"`
	OccurredAt time.Time      `json:"occurred_at"`
}

type listEventsResponse struct {
	Events     []eventResponseItem `json:"events"`
	NextCursor string              `json:"next_cursor,omitempty"`
}

func registerEventsRoutes(r chi.Router, deps AppDeps) {
	r.Get("/events", gate(deps, "event:read", handleListEvents(deps)))
}

func handleListEvents(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		kindParam := q.Get("kind")
		if kindParam != "" {
			if _, err := events.ParseKindString(kindParam); err != nil {
				badRequest(w, "invalid kind: "+kindParam+
					" (expected an operational kind from the OperationalKind proto enum"+
					" or a canonical signal type-path; one of: "+
					strings.Join(events.AllOperationalKinds(), ", ")+
					", or terminal/*, transient/*, attribute/*/changed)")
				return
			}
		}
		filter := persistence.EventListFilter{}
		if kindParam != "" {
			filter.KindIn = []string{kindParam}
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
		writeJSON(w, http.StatusOK, listEventsResponse{Events: out, NextCursor: page.NextCursor})
	}
}
