// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: writeback-bumps-progress

package runtime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type attributeWritebackBody struct {
	AttributesDelta map[string]any `json:"attributes_delta"`
}

// @decision: writeback-bumps-progress
func (c *CallbackServer) handleAttributeWriteback(w http.ResponseWriter, r *http.Request) {
	if authErr := c.authorizePeer(r); authErr != nil {
		c.Logger.Warn("attribute writeback: unauthorized",
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
		c.Logger.Warn("attribute writeback: cancel_token rejected",
			"run_id", runID.String())
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":"read body"}`, http.StatusBadRequest)
		return
	}
	var body attributeWritebackBody
	if err := json.Unmarshal(raw, &body); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if len(body.AttributesDelta) == 0 {
		http.Error(w, `{"error":"attributes_delta required"}`, http.StatusBadRequest)
		return
	}

	var (
		found    bool
		badState cascade.NodeState
	)
	if txErr := c.Persist.Transaction(r.Context(), func(ctx context.Context, tx persistence.Tx) error {
		row, err := c.Persist.Nodes().GetRunByDispatchIDForUpdate(ctx, runID, tx)
		if err != nil {
			return err
		}
		if row == nil {
			return nil
		}
		found = true
		if row.State != cascade.NodeStateRunning && row.State != cascade.NodeStateHeld {
			badState = row.State
			return nil
		}
		existing, err := c.Persist.NodeAttributes().GetByRun(ctx, runID, tx)
		if err != nil {
			return err
		}
		if existing == nil {
			if err := c.Persist.NodeAttributes().Upsert(ctx, runID, row.NodeID, body.AttributesDelta, tx); err != nil {
				return err
			}
		} else if err := c.Persist.NodeAttributes().MergeDelta(ctx, runID, body.AttributesDelta, tx); err != nil {
			return err
		}
		if _, err := c.Queue.BumpLastProgressAt(ctx, runID, time.Now().UTC(), tx); err != nil {
			return err
		}
		return c.renewClaimExpiryForRun(ctx, runID, tx)
	}); txErr != nil {
		c.Logger.Error("attribute writeback: apply failed",
			"run_id", runID.String(), "error", txErr.Error())
		http.Error(w, `{"error":"writeback_failed"}`, http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, `{"error":"unknown_run_id"}`, http.StatusNotFound)
		return
	}
	if badState != "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": "run_not_active",
			"state": string(badState),
		})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
