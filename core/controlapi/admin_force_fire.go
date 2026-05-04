// admin_force_fire.go — POST /admin/scheduled-nodes/{node_id}/force-fire.
//
// Updates rimsky_schedules.next_fire_at = now() for the named node so the
// next scheduler tick picks it up (spec §16.1, §10). The schedule_ticker
// then calls frame.EnqueueOrCoalesce per the template's frame_resolution
// mode (see docs/history/2026-04-26-frame-resolution-design.md §7.5); this
// handler does not call into the frame engine directly. Returns 204 the
// moment the row is updated; the handler does not wait for the cascade.
// Callers that need to observe the fire (e.g. the §10 smoke fixture)
// poll rimsky_nodes.state or the events table separately.
//
// Auth: relies on the global AppDeps.Auth middleware. Operators that want
// the endpoint admin-gated wire an Authenticator that checks
// X-Rimsky-Admin-Token; when no Auth is configured the route is anonymous,
// consistent with the rest of the API in pre-v1.
package controlapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// registerAdminScheduleRoutes wires POST /admin/scheduled-nodes/{node_id}/force-fire.
// Replaces the Task-32 forward-declaration in app.go.
func registerAdminScheduleRoutes(r chi.Router, deps AppDeps) {
	r.Post("/admin/scheduled-nodes/{node_id}/force-fire", handleAdminForceFire(deps))
}

func handleAdminForceFire(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		id, err := uuid.Parse(chi.URLParam(req, "node_id"))
		if err != nil {
			badRequest(w, "invalid node_id")
			return
		}
		if err := deps.Persist.Schedules().ForceFire(req.Context(), id, nil); err != nil {
			writeError(w, err)
			return
		}
		// Return 204 immediately; do not wait for the scheduler tick.
		w.WriteHeader(http.StatusNoContent)
	}
}
