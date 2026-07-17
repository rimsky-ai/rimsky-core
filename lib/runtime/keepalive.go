// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @decision: async-callback-persistent-registry
// @decision: three-dispatch-deadlines

package runtime

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func (c *CallbackServer) handleKeepalive(w http.ResponseWriter, r *http.Request) {
	if authErr := c.authorizePeer(r); authErr != nil {
		c.Logger.Warn("keepalive: unauthorized",
			"error", authErr.Error())
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	runIDStr := chi.URLParam(r, "run_id")
	parsed, err := uuid.Parse(runIDStr)
	if err != nil {
		http.Error(w, `{"error":"invalid run_id"}`, http.StatusBadRequest)
		return
	}
	runID := shared.UUID(parsed)

	var found bool
	if txErr := c.Persist.Transaction(r.Context(), func(ctx context.Context, tx persistence.Tx) error {
		var berr error
		found, berr = c.Queue.BumpLastProgressAt(ctx, tx, runID, time.Now().UTC())
		if berr != nil || !found {
			return berr
		}
		return c.renewClaimExpiryForRun(ctx, tx, runID)
	}); txErr != nil {
		c.Logger.Error("keepalive: bump failed",
			"run_id", runID.String(), "error", txErr.Error())
		http.Error(w, `{"error":"bump_failed"}`, http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, `{"error":"unknown_run_id"}`, http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// @concept: orphan-reaper
func (c *CallbackServer) renewClaimExpiryForRun(ctx context.Context, tx persistence.Tx, runID shared.UUID) error {
	if c.ClaimHandles == nil {
		return nil
	}
	interval := c.LivenessInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	newExpiry := time.Now().UTC().Add(5 * interval)
	return c.ClaimHandles.RenewExpiryForHolderRun(ctx, runID, newExpiry, tx)
}
