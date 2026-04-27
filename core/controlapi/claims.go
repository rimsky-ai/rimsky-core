// claims.go — GET /lock-holders/{lock_holder_id}/claim-holders.
//
// Returns the held-claim ledger rows for a given lock-holder (spec
// §12.11). Under stores-redesign-v2 each row simply records subgraph
// membership and per-member terminal state; the resolution actions
// live in template metadata (rimsky_templates.spec), not on the row.
//
// Read-only and unauthenticated under today's anonymous-by-default
// config; the route is gated by AppDeps.Auth when the deployer wires
// one.

package controlapi

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/fallguy/rimsky/core/storage"
)

type claimHolderResponse struct {
	ID           string     `json:"id"`
	LockHolderID string     `json:"lock_holder_id"`
	HolderNodeID string     `json:"holder_node_id"`
	State        string     `json:"state"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
}

func toClaimHolderResponse(r storage.ClaimHolderRow) claimHolderResponse {
	return claimHolderResponse{
		ID:           r.ID.String(),
		LockHolderID: r.LockHolderID.String(),
		HolderNodeID: r.HolderNodeID.String(),
		State:        string(r.State),
		CompletedAt:  r.CompletedAt,
	}
}

// registerClaimsRoutes wires GET /lock-holders/{lock_holder_id}/claim-holders.
// (Renamed from /claims/{claim_id}/holders per spec §12.11 — the
// row's identity is by lock-holder FK, not by a free-form claim_id.)
func registerClaimsRoutes(r chi.Router, deps AppDeps) {
	r.Get("/lock-holders/{lock_holder_id}/claim-holders", handleListClaimHolders(deps))
}

func handleListClaimHolders(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		raw := chi.URLParam(req, "lock_holder_id")
		if raw == "" {
			badRequest(w, "lock_holder_id is required")
			return
		}
		id, err := uuid.Parse(raw)
		if err != nil {
			badRequest(w, "lock_holder_id must be a UUID")
			return
		}
		rows, err := deps.Storage.ClaimHolders().ListByLockHolderID(req.Context(), id, nil)
		if err != nil {
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
