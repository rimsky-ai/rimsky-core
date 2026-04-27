// admin_claim_stores.go — POST /admin/claim-stores/{name}/items.
//
// Bulk-inserts items into the operator-owned items table backing a named
// claim store (spec §7.3, §9.10). Rimsky itself never enqueues into a
// claim store — this endpoint exists for operators and external producers
// who want to drive items through HTTP rather than direct SQL.
//
// Auth: relies on the global AppDeps.Auth middleware. Operators that want
// the endpoint admin-gated wire an Authenticator that checks
// X-Rimsky-Admin-Token (spec §7.3); when no Auth is configured the route
// is anonymous, consistent with the rest of the API in pre-v1.
package controlapi

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/fallguy/rimsky/core/store/claimstorepg"
)

type adminClaimStoreItem struct {
	Payload json.RawMessage `json:"payload"`
}

type adminClaimStoreInsertRequest struct {
	Items []adminClaimStoreItem `json:"items"`
}

type adminClaimStoreInsertResponse struct {
	Inserted int `json:"inserted"`
}

// registerAdminClaimStoresRoutes wires POST /admin/claim-stores/{name}/items.
// Replaces the Task-32 forward-declaration in app.go.
func registerAdminClaimStoresRoutes(r chi.Router, deps AppDeps) {
	r.Post("/admin/claim-stores/{name}/items", handleAdminClaimStoreInsert(deps))
}

func handleAdminClaimStoreInsert(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if deps.Stores == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error": "store registry not configured",
			})
			return
		}
		name := chi.URLParam(req, "name")
		if name == "" {
			badRequest(w, "store name is required")
			return
		}
		s, ok := deps.Stores.GetStore(name)
		if !ok {
			notFoundResp(w, "store not found: "+name)
			return
		}
		// Only the postgres claim store supports admin item insert; other
		// store kinds reject with 400 so misconfigured callers get a clear
		// error instead of a silent no-op.
		cs, ok := s.(*claimstorepg.Store)
		if !ok {
			badRequest(w, "store "+name+" is not a postgres claim store (kind="+s.Kind()+")")
			return
		}

		var body adminClaimStoreInsertRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			badRequest(w, "invalid JSON body: "+err.Error())
			return
		}
		if len(body.Items) == 0 {
			badRequest(w, "items array must not be empty")
			return
		}
		payloads := make([]json.RawMessage, 0, len(body.Items))
		for i, item := range body.Items {
			if len(item.Payload) == 0 {
				badRequest(w, "items[*].payload is required")
				return
			}
			if !json.Valid(item.Payload) {
				badRequest(w, "items["+strconv.Itoa(i)+"].payload is not valid JSON")
				return
			}
			payloads = append(payloads, item.Payload)
		}

		if err := cs.InsertItems(req.Context(), payloads); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, adminClaimStoreInsertResponse{
			Inserted: len(payloads),
		})
	}
}
