// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package controlapi

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type claimHolderResponse struct {
	ID              string     `json:"id"`
	ClaimHandleID   string     `json:"claim_handle_id"`
	HolderNodeRunID string     `json:"holder_run_id"`
	State           string     `json:"state"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
}

func toClaimHolderResponse(r persistence.ClaimHolderRow) claimHolderResponse {
	return claimHolderResponse{
		ID:              r.ID.String(),
		ClaimHandleID:   r.ClaimHandleID.String(),
		HolderNodeRunID: r.HolderNodeRunID.String(),
		State:           string(r.State),
		CompletedAt:     r.CompletedAt,
	}
}

func registerClaimsRoutes(r chi.Router, deps AppDeps) {
	r.Get("/claim-handles/{claim_handle_id}/holders", gate(deps, "claim-holders:read", handleListClaimHolders(deps)))
}

// @concept: claim-handle
func handleListClaimHolders(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		raw := chi.URLParam(req, "claim_handle_id")
		if raw == "" {
			badRequest(w, "claim_handle_id is required")
			return
		}
		id, err := uuid.Parse(raw)
		if err != nil {
			badRequest(w, "claim_handle_id must be a UUID")
			return
		}
		limit, err := parseLimit(req, 100)
		if err != nil {
			badRequest(w, err.Error())
			return
		}
		var rows []persistence.ClaimHolderRow
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			handle, err := deps.Persist.ClaimHandles().Get(ctx, id, tx)
			if err != nil {
				return err
			}
			if handle == nil {
				return shared.ErrClaimHandleNotFound
			}
			r, err := deps.Persist.ClaimHolders().ListByClaimHandleID(ctx, id, tx)
			rows = r
			return err
		}); err != nil {
			writeError(w, err)
			return
		}
		page, nextCursor, err := persistence.PageByKey(rows, req.URL.Query().Get("cursor"), limit,
			func(r persistence.ClaimHolderRow) string { return r.ID.String() })
		if err != nil {
			writeError(w, err)
			return
		}
		out := make([]claimHolderResponse, 0, len(page))
		for _, r := range page {
			out = append(out, toClaimHolderResponse(r))
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"holders":     out,
			"next_cursor": nextCursor,
		})
	}
}
