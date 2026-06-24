// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @decision: async-callback-persistent-registry
// @decision: three-dispatch-deadlines

package runtime

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func (c *CallbackServer) handleKeepalive(w http.ResponseWriter, r *http.Request) {
	runIDStr := chi.URLParam(r, "run_id")
	parsed, err := uuid.Parse(runIDStr)
	if err != nil {
		http.Error(w, `{"error":"invalid run_id"}`, http.StatusBadRequest)
		return
	}
	runID := shared.UUID(parsed)

	token := strings.TrimSpace(r.Header.Get("Authorization"))
	token = strings.TrimPrefix(token, "Bearer ")
	token = strings.TrimSpace(token)
	if token == "" {
		http.Error(w, `{"error":"missing authorization"}`, http.StatusUnauthorized)
		return
	}
	if authErr := c.runTokenAuth(token, runID); authErr != nil {
		c.Logger.Warn("keepalive: unauthorized",
			"run_id", runID.String(), "error", authErr.Error())
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	var found bool
	if txErr := c.Persist.Transaction(r.Context(), func(ctx context.Context, tx persistence.Tx) error {
		var berr error
		found, berr = c.Queue.BumpLastProgressAt(ctx, tx, runID, time.Now().UTC())
		return berr
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
