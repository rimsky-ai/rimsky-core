// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package controlapi

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

func registerFramesRoutes(r chi.Router, deps AppDeps) {
	r.Get("/instances/{idOrKey}/frames", gate(deps, "instance:list-frames", handleListInstanceFrames(deps)))
	r.Get("/instances/{idOrKey}/frames/{frame_id}", gate(deps, "instance:read-frame", handleGetInstanceFrame(deps)))
}

type frameItem struct {
	FrameID             string     `json:"frame_id"`
	InstanceID          string     `json:"instance_id"`
	State               string     `json:"state"`
	TriggeringMessageID string     `json:"triggering_message_id"`
	RootRunScopeID      string     `json:"root_run_scope_id"`
	StartedAt           *time.Time `json:"started_at,omitempty"`
	EndedAt             *time.Time `json:"ended_at,omitempty"`
	LastProgressAt      *time.Time `json:"last_progress_at,omitempty"`

	MessageType          string `json:"message_type,omitempty"`
	MessageSender        string `json:"message_sender,omitempty"`
	MessageSenderKind    string `json:"message_sender_kind,omitempty"`
	MessageSenderSubject string `json:"message_sender_subject,omitempty"`
}

type listFramesResponse struct {
	Frames     []frameItem `json:"frames"`
	NextCursor string      `json:"next_cursor"`
}

func handleListInstanceFrames(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		inst, err := resolveInstance(req.Context(), deps, chi.URLParam(req, "idOrKey"))
		if err != nil {
			writeError(w, err)
			return
		}
		if inst == nil {
			notFoundResp(w, shared.ErrInstanceNotFound.Error())
			return
		}
		q := req.URL.Query()
		filter := persistence.FrameListFilter{}
		instUUID := inst.ID
		filter.InstanceID = &instUUID
		if err := persistence.ApplyFrameStateQueryParam(&filter, q.Get("state")); err != nil {
			badRequest(w, err.Error())
			return
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
		limit, err := parseLimit(req, 50)
		if err != nil {
			badRequest(w, err.Error())
			return
		}
		pag := persistence.ListPagination{Cursor: q.Get("cursor"), Limit: limit}

		ctx := req.Context()
		var page persistence.PaginatedListResult[persistence.FrameRowWithMessage]
		err = deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			p, lerr := deps.Persist.Frames().ListForObservabilityWithMessage(ctx, filter, pag, tx)
			page = p
			return lerr
		})
		if err != nil {
			writeError(w, err)
			return
		}
		items := make([]frameItem, 0, len(page.Rows))
		for _, r := range page.Rows {
			items = append(items, toFrameItem(r))
		}
		writeJSON(w, http.StatusOK, listFramesResponse{Frames: items, NextCursor: page.NextCursor})
	}
}

func handleGetInstanceFrame(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		inst, err := resolveInstance(req.Context(), deps, chi.URLParam(req, "idOrKey"))
		if err != nil {
			writeError(w, err)
			return
		}
		if inst == nil {
			notFoundResp(w, shared.ErrInstanceNotFound.Error())
			return
		}
		frameIDStr := chi.URLParam(req, "frame_id")
		frameID, err := uuid.Parse(frameIDStr)
		if err != nil {
			badRequest(w, "invalid frame_id")
			return
		}
		ctx := req.Context()
		// @story: frame-origin-audit
		instUUID := inst.ID
		var row *persistence.FrameRowWithMessage
		err = deps.Persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			r, gerr := deps.Persist.Frames().GetForObservabilityWithMessage(ctx, shared.UUID(frameID), tx)
			row = r
			return gerr
		})
		if err != nil {
			writeError(w, err)
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
		FrameID:              r.FrameID.String(),
		InstanceID:           r.InstanceID.String(),
		State:                string(r.State),
		TriggeringMessageID:  r.TriggeringMessageID.String(),
		RootRunScopeID:       r.RootRunScopeID.String(),
		StartedAt:            r.StartedAt,
		EndedAt:              r.EndedAt,
		LastProgressAt:       r.LastProgressAt,
		MessageType:          r.MessageType,
		MessageSender:        r.MessageSender,
		MessageSenderKind:    r.MessageSenderKind,
		MessageSenderSubject: r.MessageSenderSubject,
	}
}
