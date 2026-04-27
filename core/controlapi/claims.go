// claims.go — GET /claims/{claim_id}/holders.
//
// Returns the held-claim ledger rows for a given claim_id (spec §9.9.3).
// One row per terminal-leaf-node identified by §11.4 at commit-of-source;
// each row carries its declared on_commit/on_give_up actions plus, once
// the row transitions to 'completed' per §5.6.4, the actual_action that
// fired.
//
// Read-only and unauthenticated under today's anonymous-by-default config;
// the route is gated by AppDeps.Auth when the deployer wires one.
package controlapi

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/fallguy/rimsky/core/storage"
)

type claimHolderResponse struct {
	ID           string     `json:"id"`
	ClaimID      string     `json:"claim_id"`
	StoreName    string     `json:"store_name"`
	HolderNodeID string     `json:"holder_node_id"`
	OnCommit     string     `json:"on_commit"`
	OnGiveUp     string     `json:"on_give_up"`
	ActualAction string     `json:"actual_action,omitempty"`
	State        string     `json:"state"`
	CreatedAt    time.Time  `json:"created_at"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
}

func toClaimHolderResponse(r storage.ClaimHolderRow) claimHolderResponse {
	out := claimHolderResponse{
		ID:           r.ID.String(),
		ClaimID:      r.ClaimID,
		StoreName:    r.StoreName,
		HolderNodeID: r.HolderNodeID.String(),
		OnCommit:     string(r.OnCommit),
		OnGiveUp:     string(r.OnGiveUp),
		State:        string(r.State),
		CreatedAt:    r.CreatedAt,
		CompletedAt:  r.CompletedAt,
	}
	if r.ActualAction != nil {
		out.ActualAction = string(*r.ActualAction)
	}
	return out
}

// registerClaimsRoutes wires GET /claims/{claim_id}/holders.
// Replaces the Task-32 forward-declaration in app.go.
func registerClaimsRoutes(r chi.Router, deps AppDeps) {
	r.Get("/claims/{claim_id}/holders", handleListClaimHolders(deps))
}

func handleListClaimHolders(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		claimID := chi.URLParam(req, "claim_id")
		if claimID == "" {
			badRequest(w, "claim_id is required")
			return
		}
		rows, err := deps.Storage.ClaimHolders().ListByClaimID(req.Context(), claimID, nil)
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
