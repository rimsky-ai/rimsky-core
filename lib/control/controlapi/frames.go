// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// frames.go — cascade-graph frames-read endpoints.
//
//   - GET /instances/{id}/frames — list frames for an instance, optionally
//     filtered by triggering_message_id (the message → frames reverse join).
//   - GET /instances/{id}/frames/{frame_id} — fetch one frame, joined with
//     its triggering message envelope (the forward join).
//
// Both endpoints surface the rimsky_frames.triggering_message_id wiring
// introduced by Pass 1 of the message-schema-layer plan, so the
// frame-origin-audit story can observe "every frame, its triggering
// message" end-to-end through the API surface.

package controlapi

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// registerFramesRoutes wires the cascade-graph frames-read endpoints.
func registerFramesRoutes(r chi.Router, deps AppDeps) {
	r.Get("/instances/{id}/frames", gate(deps, "instance:list-frames", handleListInstanceFrames(deps)))
	r.Get("/instances/{id}/frames/{frame_id}", gate(deps, "instance:read-frame", handleGetInstanceFrame(deps)))
}

// frameItem is the JSON projection of one persistence.FrameRowWithMessage
// — the frame row joined with its triggering message envelope. The
// envelope fields are flat on the response so a reader doesn't have to
// chase a nested object — the frame-origin-audit story's load-bearing
// surface is "every frame line names its triggering message."
type frameItem struct {
	FrameID             string     `json:"frame_id"`
	InstanceID          string     `json:"instance_id"`
	State               string     `json:"state"`
	TriggeringMessageID string     `json:"triggering_message_id"`
	StartedAt           *time.Time `json:"started_at,omitempty"`
	EndedAt             *time.Time `json:"ended_at,omitempty"`
	// LastProgressAt: rimsky_frames.last_progress_at, refreshed by
	// RefreshProgress on every node-state transition inside the frame.
	// Surfaced so operators reading the frame-origin-audit endpoint can
	// tell "this frame is still making progress at time T" from "this
	// frame has stopped advancing" without falling back to slog.
	LastProgressAt *time.Time `json:"last_progress_at,omitempty"`
	FrameTimeoutMs int64      `json:"frame_timeout_ms"`

	// @constraint: joined message envelope fields. Always non-empty
	// under the frame→message ON DELETE RESTRICT FK; the LEFT JOIN
	// inside the accessor degrades to empty strings rather than a query
	// error if a row ever lacks an envelope (which the FK should
	// prevent).
	MessageType       string `json:"message_type,omitempty"`
	MessageSender     string `json:"message_sender,omitempty"`
	MessageSenderKind string `json:"message_sender_kind,omitempty"`
}

type listFramesResponse struct {
	Frames     []frameItem `json:"frames"`
	NextCursor string      `json:"next_cursor,omitempty"`
}

// handleListInstanceFrames is GET /instances/{id}/frames.
//
// Query params:
//   - triggering_message_id=<uuid> — narrow to frames whose origin
//     envelope is the named message (the reverse-join surface).
//   - state=<queued|running|completed|failed> — narrow to one state.
//   - limit=<int>, cursor=<opaque> — pagination over (queued_at DESC,
//     frame_id DESC).
func handleListInstanceFrames(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		idStr := chi.URLParam(req, "id")
		instanceID, err := uuid.Parse(idStr)
		if err != nil {
			badRequest(w, "invalid instance id")
			return
		}
		q := req.URL.Query()
		filter := persistence.FrameListFilter{}
		instUUID := shared.UUID(instanceID)
		filter.InstanceID = &instUUID
		if s := q.Get("state"); s != "" {
			filter.State = persistence.FrameState(s)
		}
		if tm := q.Get("triggering_message_id"); tm != "" {
			parsed, perr := uuid.Parse(tm)
			if perr != nil {
				badRequest(w, "invalid triggering_message_id")
				return
			}
			tmUUID := shared.UUID(parsed)
			filter.TriggeringMessageID = &tmUUID
		}
		pag := persistence.ListPagination{Cursor: q.Get("cursor")}
		if l := q.Get("limit"); l != "" {
			n, perr := strconv.Atoi(l)
			if perr == nil && n > 0 {
				pag.Limit = n
			}
		}

		// @constraint: single tx covers BOTH the instance-existence
		// check and the frame list. Splitting them into two txs would
		// open a TOCTOU window where the instance can be deleted
		// between checks — the same cross-tx-read concern the LEFT JOIN
		// below avoids for the frame ↔ message pair applies to the
		// instance ↔ frames pair.
		// @deliberate: one SQL per page (after the instance lookup):
		// a LEFT JOIN against rimsky_messages inside the caller's tx.
		// A per-row Messages().Get would (a) make this N+1 (50-row page
		// → 51 round-trips) and (b) open a cross-tx read window between
		// the frame row and its message row, during which a
		// hypothetical message reaper could leave the page seeing
		// frame-then-no-message.
		ctx := req.Context()
		var (
			instExists bool
			page       persistence.PaginatedListResult[persistence.FrameRowWithMessage]
		)
		err = deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			row, gerr := deps.Persist.Instances().Get(ctx, instUUID, tx)
			if gerr != nil {
				return gerr
			}
			if row == nil {
				instExists = false
				return nil
			}
			instExists = true
			p, lerr := deps.Persist.Frames().ListForObservabilityWithMessage(ctx, filter, pag, tx)
			page = p
			return lerr
		})
		if err != nil {
			writeError(w, err)
			return
		}
		if !instExists {
			notFoundResp(w, shared.ErrInstanceNotFound.Error())
			return
		}
		items := make([]frameItem, 0, len(page.Rows))
		for _, r := range page.Rows {
			items = append(items, toFrameItem(r))
		}
		writeJSON(w, http.StatusOK, listFramesResponse{Frames: items, NextCursor: page.NextCursor})
	}
}

// handleGetInstanceFrame is GET /instances/{id}/frames/{frame_id}.
//
// Returns the frame joined with its triggering message envelope (the
// forward-join surface).
func handleGetInstanceFrame(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		idStr := chi.URLParam(req, "id")
		instanceID, err := uuid.Parse(idStr)
		if err != nil {
			badRequest(w, "invalid instance id")
			return
		}
		frameIDStr := chi.URLParam(req, "frame_id")
		frameID, err := uuid.Parse(frameIDStr)
		if err != nil {
			badRequest(w, "invalid frame_id")
			return
		}
		ctx := req.Context()
		// @constraint: single tx, single round-trip: instance-existence
		// check + frame row + joined message envelope. The instance
		// check mirrors the list endpoint above — without it, a deleted
		// instance returns "frame not found" (a frame CASCADE-removed
		// when its instance was deleted) while the list endpoint at the
		// same path returns "instance not found", giving two divergent
		// 404 surfaces. The frame-origin-audit story's load-bearing
		// property is "every frame, its triggering message,
		// end-to-end"; consistent error surfaces matter for the
		// diagnostic story too.
		// @story: frame-origin-audit
		instUUID := shared.UUID(instanceID)
		var (
			instExists bool
			row        *persistence.FrameRowWithMessage
		)
		err = deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			inst, gerr := deps.Persist.Instances().Get(ctx, instUUID, tx)
			if gerr != nil {
				return gerr
			}
			if inst == nil {
				instExists = false
				return nil
			}
			instExists = true
			r, gerr := deps.Persist.Frames().GetForObservabilityWithMessage(ctx, shared.UUID(frameID), tx)
			row = r
			return gerr
		})
		if err != nil {
			writeError(w, err)
			return
		}
		if !instExists {
			notFoundResp(w, shared.ErrInstanceNotFound.Error())
			return
		}
		if row == nil || row.InstanceID != instUUID {
			notFoundResp(w, "frame not found")
			return
		}
		writeJSON(w, http.StatusOK, toFrameItem(*row))
	}
}

func toFrameItem(r persistence.FrameRowWithMessage) frameItem {
	return frameItem{
		FrameID:             r.FrameID.String(),
		InstanceID:          r.InstanceID.String(),
		State:               string(r.State),
		TriggeringMessageID: r.TriggeringMessageID.String(),
		StartedAt:           r.StartedAt,
		EndedAt:             r.EndedAt,
		LastProgressAt:      r.LastProgressAt,
		FrameTimeoutMs:      r.FrameTimeoutMs,
		MessageType:         r.MessageType,
		MessageSender:       r.MessageSender,
		MessageSenderKind:   r.MessageSenderKind,
	}
}
