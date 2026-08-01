// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package controlapi

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

type supervisorSummary struct {
	ID              string    `json:"id"`
	Concurrency     int       `json:"concurrency"`
	ActiveNodeCount int       `json:"active_node_count"`
	RegisteredAt    time.Time `json:"registered_at"`
}

type healthResponse struct {
	Status      string              `json:"status"`
	Supervisors []supervisorSummary `json:"supervisors"`
	NodeCounts  map[string]int      `json:"node_counts"`
}

func registerHealthRoutes(r chi.Router, deps AppDeps) {
	r.Get("/health", handleHealth(deps))
}

func handleHealth(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		var sups []persistence.SupervisorRow
		var counts map[cascade.NodeState]int
		runningPerSup := map[string]int{}
		if err := deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			s, err := deps.Persist.Supervisors().List(ctx, tx)
			if err != nil {
				return err
			}
			sups = s
			c, err := deps.Persist.Nodes().CountByState(ctx, tx)
			if err != nil {
				return err
			}
			counts = c
			for _, sup := range sups {
				n, err := deps.Persist.Nodes().CountRunningForSupervisor(ctx, sup.ID, tx)
				if err != nil {
					return err
				}
				runningPerSup[sup.ID] = n
			}
			return nil
		}); err != nil {
			writeError(w, err)
			return
		}
		supOut := make([]supervisorSummary, 0, len(sups))
		for _, s := range sups {
			supOut = append(supOut, supervisorSummary{
				ID:              s.ID,
				Concurrency:     s.Concurrency,
				ActiveNodeCount: runningPerSup[s.ID],
				RegisteredAt:    s.RegisteredAt,
			})
		}
		countOut := map[string]int{
			string(cascade.NodeStatePending): counts[cascade.NodeStatePending],
			string(cascade.NodeStateFresh):   counts[cascade.NodeStateFresh],
			string(cascade.NodeStateStale):   counts[cascade.NodeStateStale],
			string(cascade.NodeStateRunning): counts[cascade.NodeStateRunning],
			string(cascade.NodeStateHeld):    counts[cascade.NodeStateHeld],
			string(cascade.NodeStateParked):  counts[cascade.NodeStateParked],
			string(cascade.NodeStateFailed):  counts[cascade.NodeStateFailed],
		}
		writeJSON(w, http.StatusOK, healthResponse{
			Status:      "ok",
			Supervisors: supOut,
			NodeCounts:  countOut,
		})
	}
}
