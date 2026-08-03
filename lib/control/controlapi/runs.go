// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package controlapi

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// @concept: node-run
// @decision: node-state-retired-from-operator-api
type runStateResponse struct {
	ID             string     `json:"id"`
	NodeID         string     `json:"node_id"`
	FrameID        string     `json:"frame_id"`
	State          string     `json:"state"`
	Terminal       bool       `json:"terminal"`
	Executor       string     `json:"executor,omitempty"`
	EnqueuedAt     time.Time  `json:"enqueued_at"`
	ClaimedBy      string     `json:"claimed_by,omitempty"`
	ClaimedAt      *time.Time `json:"claimed_at,omitempty"`
	LastProgressAt *time.Time `json:"last_progress_at,omitempty"`
	AsyncAckID     string     `json:"async_ack_id,omitempty"`
}

// @decision: node-state-retired-from-operator-api
func toRunStateResponse(row persistence.DispatchRow) runStateResponse {
	out := runStateResponse{
		ID:             row.ID.String(),
		NodeID:         row.NodeID.String(),
		FrameID:        row.FrameID.String(),
		State:          string(row.State),
		Terminal:       cascade.IsTerminal(row.State),
		EnqueuedAt:     row.EnqueuedAt,
		ClaimedAt:      row.ClaimedAt,
		LastProgressAt: row.LastProgressAt,
	}
	if row.ExecutorName != nil {
		out.Executor = *row.ExecutorName
	}
	if row.ClaimedBy != nil {
		out.ClaimedBy = *row.ClaimedBy
	}
	if row.AsyncAckID != nil {
		out.AsyncAckID = *row.AsyncAckID
	}
	return out
}

func registerRunsRoutes(r chi.Router, deps AppDeps) {
	r.Get("/runs/{run_id}", gate(deps, "run:read", handleGetRunState(deps)))
}

// @concept: node-run
// @decision: node-state-retired-from-operator-api
func handleGetRunState(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		raw := chi.URLParam(req, "run_id")
		if raw == "" {
			badRequest(w, "run_id is required")
			return
		}
		id, err := uuid.Parse(raw)
		if err != nil {
			badRequest(w, "run_id must be a UUID")
			return
		}
		row, err := deps.Queue.GetAnyByID(req.Context(), shared.UUID(id))
		if err != nil {
			writeError(w, err)
			return
		}
		if row == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "run not found: " + id.String()})
			return
		}
		writeJSON(w, http.StatusOK, toRunStateResponse(*row))
	}
}
