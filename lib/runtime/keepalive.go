// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: async-callback-persistent-registry
// @decision: three-dispatch-deadlines
// @decision: keepalive-endpoint

package runtime

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// @concept: executor
func (c *CallbackServer) authorizeCancelToken(r *http.Request, runID shared.UUID) bool {
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return false
	}
	presented := strings.TrimPrefix(auth, prefix)
	expected := c.SupervisorID + ":" + runID.String()
	return subtle.ConstantTimeCompare([]byte(presented), []byte(expected)) == 1
}

// @decision: keepalive-endpoint
func (c *CallbackServer) handleKeepalive(w http.ResponseWriter, r *http.Request) {
	if authErr := c.authorizeService(r); authErr != nil {
		c.Logger.Warn("KEEPALIVE.REQUEST.UNAUTHORIZED",
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
	if !c.authorizeCancelToken(r, runID) {
		c.Logger.Warn("KEEPALIVE.CANCELTOKEN.REJECTED",
			"run_id", runID.String())
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	var found bool
	if txErr := c.Persist.Transaction(r.Context(), func(ctx context.Context, tx persistence.Tx) error {
		var berr error
		found, berr = c.Queue.BumpLastProgressAt(ctx, runID, c.clockNow().UTC(), tx)
		if berr != nil || !found {
			return berr
		}
		return c.renewClaimExpiryForRun(ctx, runID, tx)
	}); txErr != nil {
		c.Logger.Error("KEEPALIVE.DEADLINE.BUMPFAILED",
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
func (c *CallbackServer) renewClaimExpiryForRun(ctx context.Context, runID shared.UUID, tx persistence.Tx) error {
	if c.ClaimHandles == nil {
		return nil
	}
	interval := c.LivenessInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	newExpiry := claimExpiryFromLiveness(c.clockNow().UTC(), interval)
	return c.ClaimHandles.RenewExpiryForHolderRun(ctx, runID, c.SupervisorID, newExpiry, tx)
}

func (c *CallbackServer) clockNow() time.Time {
	if c.Clock != nil {
		return c.Clock.Now()
	}
	return time.Now()
}
