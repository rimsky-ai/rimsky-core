// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Keepalive callback handler — spec §12.6.
//
// Wire shape:
//
//	POST {callback_url}/v1/runs/{run_id}/keepalive
//	Authorization: <cancel_token>          (matches §12.4 async-callback auth)
//	→ 204 No Content
//
// The executor uses this endpoint to push the quiet-period sweep out
// without writing attributes or scratch — a pure liveness ping. The
// handler validates the supervisor-issued cancel_token, bumps
// col:rimsky_node_runs.last_progress_at to `now`, and returns 204.
//
// @concept: async-callback-persistence
// @decision: dispatch-deadlines

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

// handleKeepalive is the chi handler for POST /v1/runs/{run_id}/keepalive.
//
// @agent-contract:
//   - What: authenticates the caller via the §12.4 cancel_token shape and
//     bumps col:rimsky_node_runs.last_progress_at so the
//     code:SweepExecutorDeadlines quiet-period sweep counts from now.
//   - Auth: reuses c.attributesAuth (the §12.5 writeback's auth), so a
//     leaked attributes-callback token authorises keepalive on the same
//     run and nothing else — the same scope per cancel_token everywhere.
//   - Status codes: 204 on success; 400 on unparseable run_id; 401 on
//     missing or rejected token; 404 on unknown run; 500 on persistence
//     failure.
//   - Does NOT: write attributes, write scratch, drive the cascade,
//     change phase, or extend any other timestamp. The §12.5 attributes
//     callback and the scratch callback bump last_progress_at as a
//     side effect of their primary write — code:handleKeepalive is the
//     standalone path for executors that have nothing to write but
//     still need the quiet-period sweep pushed out.
func (c *CallbackServer) handleKeepalive(w http.ResponseWriter, r *http.Request) {
	runIDStr := chi.URLParam(r, "run_id")
	parsed, err := uuid.Parse(runIDStr)
	if err != nil {
		http.Error(w, `{"error":"invalid run_id"}`, http.StatusBadRequest)
		return
	}
	runID := shared.UUID(parsed)

	token := strings.TrimSpace(r.Header.Get("Authorization"))
	// @deliberate: same Bearer-tolerance as the §12.5 attributes handler;
	// executors construct one Authorization header for both endpoints.
	token = strings.TrimPrefix(token, "Bearer ")
	token = strings.TrimSpace(token)
	if token == "" {
		http.Error(w, `{"error":"missing authorization"}`, http.StatusUnauthorized)
		return
	}
	if authErr := c.attributesAuth(token, runID); authErr != nil {
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
		// @constraint: the dispatch row is gone (terminal already landed
		// and the row was reaped). Returning 404 lets the executor stop
		// polling instead of looping on a settled run.
		http.Error(w, `{"error":"unknown_run_id"}`, http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
