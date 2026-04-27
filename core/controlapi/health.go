// health.go — GET /health. Returns a shallow liveness snapshot: the API is
// ok if it can query supervisors and node counts; the response also surfaces
// registered supervisors and node-state rollup so operators can eyeball the
// cluster without a deeper query.
package controlapi

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/fallguy/rimsky/core/shared"
)

type supervisorSummary struct {
	ID                string    `json:"id"`
	AcceptedExecutors []string  `json:"accepted_executors"`
	Concurrency       int       `json:"concurrency"`
	ActiveNodeCount   int       `json:"active_node_count"`
	LastHeartbeatAt   time.Time `json:"last_heartbeat_at"`
}

type healthResponse struct {
	Status      string              `json:"status"`
	Supervisors []supervisorSummary `json:"supervisors"`
	NodeCounts  map[string]int      `json:"node_counts"`
}

// registerHealthRoutes wires GET /health.
func registerHealthRoutes(r chi.Router, deps AppDeps) {
	r.Get("/health", handleHealth(deps))
}

func handleHealth(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		sups, err := deps.Storage.Supervisors().List(req.Context(), nil)
		if err != nil {
			writeError(w, err)
			return
		}
		counts, err := deps.Storage.Nodes().CountByState(req.Context(), nil)
		if err != nil {
			writeError(w, err)
			return
		}
		supOut := make([]supervisorSummary, 0, len(sups))
		for _, s := range sups {
			supOut = append(supOut, supervisorSummary{
				ID:                s.ID,
				AcceptedExecutors: s.AcceptedExecutors,
				Concurrency:       s.Concurrency,
				ActiveNodeCount:   s.ActiveNodeCount,
				LastHeartbeatAt:   s.LastHeartbeatAt,
			})
		}
		countOut := map[string]int{
			string(shared.NodeStateFresh):   counts[shared.NodeStateFresh],
			string(shared.NodeStateStale):   counts[shared.NodeStateStale],
			string(shared.NodeStateRunning): counts[shared.NodeStateRunning],
			string(shared.NodeStateFailed):  counts[shared.NodeStateFailed],
		}
		writeJSON(w, http.StatusOK, healthResponse{
			Status:      "ok",
			Supervisors: supOut,
			NodeCounts:  countOut,
		})
	}
}
