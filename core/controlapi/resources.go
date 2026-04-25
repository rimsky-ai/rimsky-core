// resources.go — GET /resources/:id/current,
// GET /resources/:id/versions, GET /resources/:id/versions/:version_id.
package controlapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/fallguy/rimsky/core/storage"
)

type resourceVersionResponse struct {
	ID            string    `json:"id"`
	ResourceID    string    `json:"resource_id"`
	ProducedBy    string    `json:"produced_by,omitempty"`
	Data          any       `json:"data,omitempty"`
	DataRef       any       `json:"data_ref,omitempty"`
	ChangeSummary string    `json:"change_summary,omitempty"`
	CommittedAt   time.Time `json:"committed_at"`
}

func toVersionResponse(v storage.ResourceVersionRow) resourceVersionResponse {
	out := resourceVersionResponse{
		ID:            v.ID.String(),
		ResourceID:    v.ResourceID.String(),
		ChangeSummary: v.ChangeSummary,
		CommittedAt:   v.CommittedAt,
	}
	if v.ProducedBy != nil {
		out.ProducedBy = v.ProducedBy.String()
	}
	if len(v.Data) > 0 {
		var decoded any
		if err := json.Unmarshal(v.Data, &decoded); err == nil {
			out.Data = decoded
		}
	}
	if len(v.DataRef) > 0 {
		var decoded any
		if err := json.Unmarshal(v.DataRef, &decoded); err == nil {
			out.DataRef = decoded
		} else {
			out.DataRef = string(v.DataRef)
		}
	}
	return out
}

// registerResourcesRoutes wires the /resources group.
func registerResourcesRoutes(r chi.Router, deps AppDeps) {
	r.Get("/resources/{id}/current", handleGetCurrentVersion(deps))
	r.Get("/resources/{id}/versions", handleListVersions(deps))
	r.Get("/resources/{id}/versions/{version_id}", handleGetVersion(deps))
}

func handleGetCurrentVersion(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		id, err := uuid.Parse(chi.URLParam(req, "id"))
		if err != nil {
			badRequest(w, "invalid id")
			return
		}
		row, err := deps.Storage.Resources().Get(req.Context(), id, nil)
		if err != nil {
			writeError(w, err)
			return
		}
		if row == nil {
			notFoundResp(w, "resource not found")
			return
		}
		if row.CurrentVersionID == nil {
			notFoundResp(w, "no current version")
			return
		}
		v, err := deps.Storage.Resources().GetVersion(req.Context(), *row.CurrentVersionID, nil)
		if err != nil {
			writeError(w, err)
			return
		}
		if v == nil {
			notFoundResp(w, "no current version")
			return
		}
		writeJSON(w, http.StatusOK, toVersionResponse(*v))
	}
}

func handleListVersions(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		id, err := uuid.Parse(chi.URLParam(req, "id"))
		if err != nil {
			badRequest(w, "invalid id")
			return
		}
		cursor := req.URL.Query().Get("cursor")
		limit := parseLimit(req, 100)
		page, err := deps.Storage.Resources().ListVersionsPaged(req.Context(), id,
			storage.ListPagination{Limit: limit, Cursor: cursor}, nil)
		if err != nil {
			writeError(w, err)
			return
		}
		items := make([]resourceVersionResponse, 0, len(page.Rows))
		for _, v := range page.Rows {
			items = append(items, toVersionResponse(v))
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"versions":    items,
			"next_cursor": page.NextCursor,
		})
	}
}

func handleGetVersion(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if _, err := uuid.Parse(chi.URLParam(req, "id")); err != nil {
			badRequest(w, "invalid resource id")
			return
		}
		versionID, err := uuid.Parse(chi.URLParam(req, "version_id"))
		if err != nil {
			badRequest(w, "invalid version_id")
			return
		}
		v, err := deps.Storage.Resources().GetVersion(req.Context(), versionID, nil)
		if err != nil {
			writeError(w, err)
			return
		}
		if v == nil {
			notFoundResp(w, "version not found")
			return
		}
		writeJSON(w, http.StatusOK, toVersionResponse(*v))
	}
}
