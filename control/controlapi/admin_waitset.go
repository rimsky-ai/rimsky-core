// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// admin_waitset.go — GET /admin/diagnostics/wait-sets.
//
// Surfaces the rimsky_wait_set ledger so operators can debug stuck
// frames ("which receiver is gated on which sender right now?").
//
//	@concept: wait-set
package controlapi

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
)

// WaitSetEntry is one wait-set row surfaced via /admin/diagnostics/wait-sets.
// Post-stage-5 of the run-row lifecycle cutover, the receiver / sender
// columns key on rimsky_node_runs(id) — see migration
// `005-claim-holders-wait-set-run-level.sql`.
type WaitSetEntry struct {
	FrameID           uuid.UUID `json:"frame_id"`
	ReceiverRunID     uuid.UUID `json:"receiver_run_id"`
	SenderRunID       uuid.UUID `json:"sender_run_id"`
	TopicKind         string    `json:"topic_kind"`
	SubscriptionScope string    `json:"subscription_scope"`
	TopicFilter       any       `json:"topic_filter,omitempty"`
}

// WaitSetResponse is the body of GET /admin/diagnostics/wait-sets.
type WaitSetResponse struct {
	WaitSet []WaitSetEntry `json:"wait_set"`
}

// handleAdminWaitSets handles GET /admin/diagnostics/wait-sets.
// Required query param: frame=<uuid>. Optional: receiver_run=<uuid>
// (filter to rows where the receiver_run_id matches). Post-stage-5 of
// the run-row lifecycle cutover, the legacy `node=<uuid>` parameter
// remains accepted as an alias for `receiver_run` so operators driving
// the endpoint from saved tooling don't see a hard break, but the
// underlying ledger keys on run id.
func handleAdminWaitSets(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		frameStr := req.URL.Query().Get("frame")
		runStr := req.URL.Query().Get("receiver_run")
		if runStr == "" {
			runStr = req.URL.Query().Get("node")
		}
		if frameStr == "" {
			badRequest(w, "missing required ?frame= query param")
			return
		}
		frameID, err := uuid.Parse(frameStr)
		if err != nil {
			badRequest(w, "invalid frame id")
			return
		}
		var receiver *uuid.UUID
		if runStr != "" {
			rid, err := uuid.Parse(runStr)
			if err != nil {
				badRequest(w, "invalid receiver_run id")
				return
			}
			receiver = &rid
		}
		out := WaitSetResponse{WaitSet: []WaitSetEntry{}}
		err = deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			var rows []persistence.WaitSetRow
			var err error
			if receiver != nil {
				rows, err = deps.Persist.WaitSet().ListForReceiver(ctx, frameID, *receiver, tx)
			} else {
				rows, err = deps.Persist.WaitSet().ListForFrame(ctx, frameID, tx)
			}
			if err != nil {
				return err
			}
			for _, r := range rows {
				entry := WaitSetEntry{
					FrameID:           r.FrameID,
					ReceiverRunID:     r.ReceiverRunID,
					SenderRunID:       r.SenderRunID,
					TopicKind:         r.TopicKind,
					SubscriptionScope: r.SubscriptionScope,
				}
				if len(r.TopicFilter) > 0 {
					var f any
					_ = json.Unmarshal(r.TopicFilter, &f)
					entry.TopicFilter = f
				}
				out.WaitSet = append(out.WaitSet, entry)
			}
			return nil
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}
