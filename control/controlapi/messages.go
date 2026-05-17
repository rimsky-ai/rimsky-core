// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// messages.go — F1, F2. Unified message-layer endpoints.
//
// Spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Messages / Control-api endpoints.
//
//   - POST /instances/{id}/messages       — operator-side enqueue.
//   - GET  /instances/{id}/messages       — paginated list.
//   - GET  /messages/{id}                  — single message detail.
//
// @concept: message
//
// Payload bytes are inert per @blessed-invariant 21: the handler
// never logs or formats `payload`; readers carry through to the wire
// unchanged.
package controlapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/runtime"
)

// registerMessagesRoutes wires the message endpoints.
func registerMessagesRoutes(r chi.Router, deps AppDeps) {
	r.Post("/instances/{id}/messages", handleCreateMessage(deps))
	r.Get("/instances/{id}/messages", handleListInstanceMessages(deps))
	r.Get("/messages/{id}", handleGetMessage(deps))
}

// postMessageRequest is the body shape of POST /instances/{id}/messages.
//
// Per spec §Messages / Envelope, `sender` is derived from caller
// identity — V1 supplies "operator" because cross-instance senders are
// V2. `sender_kind` is always "operator" for this endpoint.
type postMessageRequest struct {
	Kind    string          `json:"kind"`
	Target  string          `json:"target,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type postMessageResponse struct {
	MessageID string `json:"message_id"`
}

// messageItem is the JSON projection of persistence.MessageRow. Payload
// is forwarded as-is per @blessed-invariant 21 — the bytes flow from
// row to JSON without inspection.
type messageItem struct {
	ID                  string          `json:"id"`
	InstanceID          string          `json:"instance_id"`
	Kind                string          `json:"kind"`
	Sender              string          `json:"sender"`
	SenderKind          string          `json:"sender_kind"`
	Target              string          `json:"target,omitempty"`
	Payload             json.RawMessage `json:"payload,omitempty"`
	BackfillOperationID string          `json:"backfill_operation_id,omitempty"`
	ReceivedAt          time.Time       `json:"received_at"`
	DeliveredAt         *time.Time      `json:"delivered_at,omitempty"`
	FrameID             string          `json:"frame_id,omitempty"`
	Cancelled           bool            `json:"cancelled,omitempty"`
}

func toMessageItem(r persistence.MessageRow) messageItem {
	out := messageItem{
		ID:          r.ID.String(),
		InstanceID:  r.InstanceID.String(),
		Kind:        r.Kind,
		Sender:      r.Sender,
		SenderKind:  r.SenderKind,
		Target:      r.Target,
		Payload:     r.Payload,
		ReceivedAt:  r.ReceivedAt,
		DeliveredAt: r.DeliveredAt,
		Cancelled:   r.Cancelled,
	}
	if r.BackfillOperationID != nil {
		out.BackfillOperationID = r.BackfillOperationID.String()
	}
	if r.FrameID != nil {
		out.FrameID = r.FrameID.String()
	}
	return out
}

// handleCreateMessage is POST /instances/{id}/messages.
//
// Validates the body, ensures the instance exists and is not
// terminated, then enqueues via runtime.EnqueueMessage. Returns the
// message id.
func handleCreateMessage(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		idStr := chi.URLParam(req, "id")
		instanceID, err := uuid.Parse(idStr)
		if err != nil {
			badRequest(w, "invalid instance id")
			return
		}
		var body postMessageRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			badRequest(w, "invalid JSON body: "+err.Error())
			return
		}
		if body.Kind == "" {
			badRequest(w, "kind is required")
			return
		}
		// V1 only supports the `invalidate` kind; cross-instance kinds
		// are V2. Reject unknown kinds at the boundary so operators get
		// a precise error instead of a silent dead-letter.
		if body.Kind != "invalidate" {
			badRequest(w, "kind must be 'invalidate' in V1")
			return
		}
		// Resolve instance existence + active-state inside the same tx
		// as the enqueue so concurrent terminations are observed.
		msgID := shared.UUID(uuid.New())
		enqueueReq := persistence.EnqueueMessageRequest{
			ID:         msgID,
			InstanceID: shared.UUID(instanceID),
			Kind:       body.Kind,
			Sender:     "operator",
			SenderKind: "operator",
			Target:     body.Target,
			Payload:    body.Payload,
		}
		err = deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			inst, err := deps.Persist.Instances().Get(ctx, shared.UUID(instanceID), tx)
			if err != nil {
				return err
			}
			if inst == nil {
				return shared.ErrInstanceNotFound
			}
			if inst.TerminatedAt != nil {
				return errInstanceTerminated
			}
			return runtime.EnqueueMessage(ctx, tx, deps.Persist.Messages(), enqueueReq)
		})
		if err != nil {
			if errors.Is(err, shared.ErrInstanceNotFound) {
				notFoundResp(w, shared.ErrInstanceNotFound.Error())
				return
			}
			if errors.Is(err, errInstanceTerminated) {
				writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
				return
			}
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, postMessageResponse{MessageID: msgID.String()})
	}
}

// errInstanceTerminated is the sentinel returned when the message
// target instance has already terminated. Mapped to 409 Conflict.
var errInstanceTerminated = errors.New("instance has terminated; no further messages accepted")

// handleListInstanceMessages is GET /instances/{id}/messages.
//
// Query params: `kind`, `sender_kind`, `target`,
// `backfill_operation_id`, `delivered_after`, `delivered_before`,
// `limit`, `cursor`. Each filter is optional; all share AND semantics.
func handleListInstanceMessages(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		idStr := chi.URLParam(req, "id")
		instanceID, err := uuid.Parse(idStr)
		if err != nil {
			badRequest(w, "invalid instance id")
			return
		}
		q := req.URL.Query()
		instUUID := shared.UUID(instanceID)
		filter := persistence.MessageListFilter{
			InstanceID: &instUUID,
			Kind:       q.Get("kind"),
			SenderKind: q.Get("sender_kind"),
			Target:     q.Get("target"),
		}
		if s := q.Get("backfill_operation_id"); s != "" {
			opID, err := uuid.Parse(s)
			if err != nil {
				badRequest(w, "invalid backfill_operation_id")
				return
			}
			u := shared.UUID(opID)
			filter.BackfillOperationID = &u
		}
		if s := q.Get("delivered_after"); s != "" {
			t, err := time.Parse(time.RFC3339, s)
			if err != nil {
				badRequest(w, "invalid delivered_after (RFC3339 required)")
				return
			}
			filter.DeliveredAfter = &t
		}
		if s := q.Get("delivered_before"); s != "" {
			t, err := time.Parse(time.RFC3339, s)
			if err != nil {
				badRequest(w, "invalid delivered_before (RFC3339 required)")
				return
			}
			filter.DeliveredBefore = &t
		}
		pag := persistence.ListPagination{
			Limit:  parseLimit(req, 100),
			Cursor: q.Get("cursor"),
		}
		page, err := deps.Persist.Messages().List(req.Context(), filter, pag)
		if err != nil {
			writeError(w, err)
			return
		}
		items := make([]messageItem, 0, len(page.Rows))
		for _, r := range page.Rows {
			items = append(items, toMessageItem(r))
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"messages":    items,
			"next_cursor": page.NextCursor,
		})
	}
}

// handleGetMessage is GET /messages/{id}.
//
// @blessed-invariant: messages are inert in rimsky. The persistence-
// layer fetch here is one of two sanctioned read sites for message
// payload bytes (the other is the substitution-leaf walk in
// graph/attribute/substitution.go::resolveTrigger). Rimsky never logs,
// formats with `%v`, validates beyond schema gates, transforms, or
// includes payload bytes in error messages. Same opacity discipline as
// `@blessed-invariant 11/20/21`.
func handleGetMessage(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		idStr := chi.URLParam(req, "id")
		id, err := uuid.Parse(idStr)
		if err != nil {
			badRequest(w, "invalid message id")
			return
		}
		row, err := deps.Persist.Messages().Get(req.Context(), shared.UUID(id))
		if err != nil {
			writeError(w, err)
			return
		}
		if row == nil {
			notFoundResp(w, "message not found")
			return
		}
		writeJSON(w, http.StatusOK, toMessageItem(*row))
	}
}
