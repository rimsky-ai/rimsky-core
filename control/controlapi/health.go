// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// health.go — GET /health. Returns a shallow liveness snapshot: the API is
// ok if it can query supervisors and node counts; the response also surfaces
// registered supervisors and node-state rollup so operators can eyeball the
// cluster without a deeper query.
package controlapi

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/fallguyconsulting/rimsky/foundation/cascade"
	"github.com/fallguyconsulting/rimsky/foundation/persistence"
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
		var sups []persistence.SupervisorRow
		var counts map[cascade.NodeState]int
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			s, err := deps.Persist.Supervisors().List(ctx, tx)
			if err != nil {
				return err
			}
			sups = s
			c, err := deps.Persist.Nodes().CountByState(ctx, tx)
			counts = c
			return err
		}); err != nil {
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
			string(cascade.NodeStateFresh):   counts[cascade.NodeStateFresh],
			string(cascade.NodeStateStale):   counts[cascade.NodeStateStale],
			string(cascade.NodeStateRunning): counts[cascade.NodeStateRunning],
			string(cascade.NodeStateFailed):  counts[cascade.NodeStateFailed],
		}
		writeJSON(w, http.StatusOK, healthResponse{
			Status:      "ok",
			Supervisors: supOut,
			NodeCounts:  countOut,
		})
	}
}
