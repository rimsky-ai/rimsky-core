// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// claims.go — GET /lock-holders/{claim_handle_id}/claim-holders.
//
// Returns the held-claim ledger rows for a given lock-holder. Under
// the stores redesign each row simply records subgraph membership and
// per-member terminal state; the resolution actions live in template
// metadata (rimsky_templates.spec), not on the row.
//
// Read-only and unauthenticated under today's anonymous-by-default
// config; the route is gated by AppDeps.Auth when the deployer wires
// one.

package controlapi

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/foundation/persistence"
)

type claimHolderResponse struct {
	ID            string     `json:"id"`
	ClaimHandleID string     `json:"claim_handle_id"`
	HolderRunID   string     `json:"holder_run_id"`
	State         string     `json:"state"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
}

func toClaimHolderResponse(r persistence.ClaimHolderRow) claimHolderResponse {
	return claimHolderResponse{
		ID:            r.ID.String(),
		ClaimHandleID: r.ClaimHandleID.String(),
		HolderRunID:   r.HolderRunID.String(),
		State:         string(r.State),
		CompletedAt:   r.CompletedAt,
	}
}

// registerClaimsRoutes wires GET /lock-holders/{claim_handle_id}/claim-holders.
// (Renamed from /claims/{claim_id}/holders per spec §12.11 — the
// row's identity is by lock-holder FK, not by a free-form claim_id.)
func registerClaimsRoutes(r chi.Router, deps AppDeps) {
	r.Get("/lock-holders/{claim_handle_id}/claim-holders", gate(deps, "claim-holders:read", handleListClaimHolders(deps)))
}

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
		var rows []persistence.ClaimHolderRow
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			r, err := deps.Persist.ClaimHolders().ListByClaimHandleID(ctx, id, tx)
			rows = r
			return err
		}); err != nil {
			writeError(w, err)
			return
		}
		out := make([]claimHolderResponse, 0, len(rows))
		for _, r := range rows {
			out = append(out, toClaimHolderResponse(r))
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"holders": out,
		})
	}
}
