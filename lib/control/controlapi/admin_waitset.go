// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: wait-set
package controlapi

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

type WaitSetEntry struct {
	FrameID           uuid.UUID `json:"frame_id"`
	ReceiverNodeRunID uuid.UUID `json:"receiver_run_id"`
	SenderNodeRunID   uuid.UUID `json:"sender_run_id"`
	TopicKind         string    `json:"topic_kind"`
	TopicFilter       any       `json:"topic_filter,omitempty"`
}

type WaitSetResponse struct {
	WaitSet []WaitSetEntry `json:"wait_set"`
}

func handleAdminWaitSets(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		frameStr := req.URL.Query().Get("frame")
		runStr := req.URL.Query().Get("receiver_run")
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
					ReceiverNodeRunID: r.ReceiverNodeRunID,
					SenderNodeRunID:   r.SenderNodeRunID,
					TopicKind:         r.TopicKind,
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
