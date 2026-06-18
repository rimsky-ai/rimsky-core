// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/frame"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
)

const senderSubjectAnonymous = "anonymous"

func operatorSenderSubject(ident auth.Identity) string {
	if ident.KeyID != nil {
		return ident.KeyID.String()
	}
	if ident.Kind == auth.IdentityAnonymous {
		return senderSubjectAnonymous
	}
	return ""
}

func dedupSenderKind(wireSenderKind string, ident auth.Identity) string {
	if wireSenderKind == "publisher" {
		return "publisher"
	}
	if ident.Kind == auth.IdentityAnonymous {
		return "anonymous"
	}
	return "operator"
}

func registerMessagesRoutes(r chi.Router, deps AppDeps) {
	r.Post("/instances/{id}/messages", gate(deps, "message:send", handleCreateMessage(deps)))
	r.Get("/instances/{id}/messages", gate(deps, "message:read", handleListInstanceMessages(deps)))
	r.Get("/messages/{id}", gate(deps, "message:read", handleGetMessage(deps)))
}

type postMessageRequest struct {
	Type                    string          `json:"type"`
	Payload                 json.RawMessage `json:"payload,omitempty"`
	Sender                  string          `json:"sender,omitempty"`
	SenderKind              string          `json:"sender_kind,omitempty"`
	PublisherSubscriptionID string          `json:"publisher_subscription_id,omitempty"`
}

type postMessageResponse struct {
	MessageID string `json:"message_id"`
}

type messageItem struct {
	ID          string          `json:"id"`
	InstanceID  string          `json:"instance_id"`
	Type        string          `json:"type"`
	Sender      string          `json:"sender"`
	SenderKind  string          `json:"sender_kind"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	ReceivedAt  time.Time       `json:"received_at"`
	DeliveredAt *time.Time      `json:"delivered_at,omitempty"`
	FrameID     string          `json:"frame_id,omitempty"`
	Cancelled   bool            `json:"cancelled,omitempty"`
}

func toMessageItem(r persistence.MessageRow) messageItem {
	out := messageItem{
		ID:          r.ID.String(),
		InstanceID:  r.InstanceID.String(),
		Type:        r.Type,
		Sender:      r.Sender,
		SenderKind:  r.SenderKind,
		Payload:     r.Payload,
		ReceivedAt:  r.ReceivedAt,
		DeliveredAt: r.DeliveredAt,
		Cancelled:   r.Cancelled,
	}
	if r.FrameID != nil {
		out.FrameID = r.FrameID.String()
	}
	return out
}

var errPublisherSubscriptionNotLive = errors.New("publisher-subscription not live (active or mounting) for this instance")

// @concept: message-schema
type unknownMessageTypeError struct {
	Type     string
	Declared []string
}

func (e *unknownMessageTypeError) Error() string {
	return fmt.Sprintf("unknown message type %q (declared types: %v)", e.Type, e.Declared)
}

func handleCreateMessage(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		idStr := chi.URLParam(req, "id")
		instanceID, err := uuid.Parse(idStr)
		if err != nil {
			badRequest(w, "invalid instance id")
			return
		}
		var body postMessageRequest
		dec := json.NewDecoder(req.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			badRequest(w, "invalid JSON body: "+err.Error())
			return
		}
		senderKind := body.SenderKind
		if senderKind == "" {
			senderKind = "operator"
		}
		if senderKind != "operator" && senderKind != "publisher" {
			badRequest(w, "sender_kind must be 'operator' or 'publisher'")
			return
		}
		if senderKind == "publisher" && body.PublisherSubscriptionID == "" {
			badRequest(w, "publisher_subscription_id required for sender_kind=publisher")
			return
		}
		sender := "operator"
		idempotencyKey := strings.TrimSpace(req.Header.Get("Idempotency-Key"))
		if idempotencyKey == "" {
			badRequest(w, "Idempotency-Key header is required")
			return
		}

		isDryRun := ModeFromContext(req.Context()) == authModeDryRun
		msgID := shared.UUID(uuid.New())
		instUUID := shared.UUID(instanceID)
		ident, _ := IdentityFromContextOK(req.Context())
		senderSubject := operatorSenderSubject(ident)
		var finalMessageID = msgID
		var replayed bool
		err = deps.Persist.Transaction(req.Context(), func(ctx context.Context, tx persistence.Tx) error {
			inst, err := deps.Persist.Instances().Get(ctx, instUUID, tx)
			if err != nil {
				return err
			}
			if inst == nil {
				return shared.ErrInstanceNotFound
			}
			if inst.TerminatedAt != nil {
				return errInstanceTerminated
			}
			// @concept: message-schema
			tpl, err := deps.Persist.Templates().GetByHash(ctx, inst.TemplateHash, tx)
			if err != nil {
				return err
			}
			if tpl == nil {
				return fmt.Errorf("instance %s template %s not found", instUUID, inst.TemplateHash)
			}
			declared := make([]string, 0, len(tpl.Spec.Messages))
			matched := false
			for _, m := range tpl.Spec.Messages {
				declared = append(declared, m.Type)
				if m.Type == body.Type {
					matched = true
				}
			}
			// @decision: empty-message-as-root-trigger
			// @story: empty-message-wakes-roots
			if body.Type == "" {
				matched = true
			}
			if !matched {
				sort.Strings(declared)
				return &unknownMessageTypeError{Type: body.Type, Declared: declared}
			}
			if senderKind == "publisher" {
				subID, parseErr := uuid.Parse(body.PublisherSubscriptionID)
				if parseErr != nil {
					return errPublisherSubscriptionNotLive
				}
				row, err := deps.Persist.PublisherSubscriptions().Get(ctx, tx, shared.UUID(subID))
				if err != nil {
					return err
				}
				live := row != nil &&
					(row.State == persistence.PublisherSubscriptionStateActive ||
						row.State == persistence.PublisherSubscriptionStateMounting)
				if !live || row.InstanceID != instUUID {
					return errPublisherSubscriptionNotLive
				}
				sender = row.PublisherName
				senderSubject = ""
			}
			if isDryRun {
				return errDryRunOK
			}
			dedupRow, inserted, err := deps.Persist.MessageIdempotencies().InsertOrLookup(ctx, tx, persistence.MessageIdempotencyRow{
				InstanceID:     instUUID,
				SenderKind:     dedupSenderKind(senderKind, ident),
				Sender:         sender,
				SenderSubject:  senderSubject,
				IdempotencyKey: idempotencyKey,
				MessageID:      msgID,
			})
			if err != nil {
				return err
			}
			if !inserted {
				finalMessageID = dedupRow.MessageID
				replayed = true
				return nil
			}
			enqueueReq := persistence.EnqueueMessageRequest{
				ID:         msgID,
				InstanceID: instUUID,
				Type:       body.Type,
				Sender:     sender,
				SenderKind: senderKind,
				Payload:    body.Payload,
			}
			if err := runtime.EnqueueMessage(ctx, tx, deps.Persist.Messages(), enqueueReq); err != nil {
				return err
			}
			_, frErr := frame.EnqueueFrame(ctx, deps.Persist, tx, instUUID, shared.UUID(msgID))
			return frErr
		})
		if isDryRun && errors.Is(err, errDryRunOK) {
			WriteDryRunResponseForced(w, "would_have_sent", map[string]any{
				"instance_id":  instanceID.String(),
				"message_type": body.Type,
				"sender_kind":  senderKind,
			})
			return
		}
		if err != nil {
			if errors.Is(err, shared.ErrInstanceNotFound) {
				notFoundResp(w, shared.ErrInstanceNotFound.Error())
				return
			}
			if errors.Is(err, errInstanceTerminated) {
				writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
				return
			}
			if errors.Is(err, errPublisherSubscriptionNotLive) {
				writeJSON(w, http.StatusForbidden, map[string]any{"error": err.Error()})
				return
			}
			var unknownType *unknownMessageTypeError
			if errors.As(err, &unknownType) {
				declared := unknownType.Declared
				if declared == nil {
					declared = []string{}
				}
				// @decision: empty-message-as-root-trigger
				// @story: empty-message-wakes-roots
				writeJSON(w, http.StatusBadRequest, map[string]any{
					"error":          "unknown message type",
					"type":           unknownType.Type,
					"declared_types": declared,
					"implicit_types": []string{""},
				})
				return
			}
			writeError(w, err)
			return
		}
		status := http.StatusCreated
		if replayed {
			status = http.StatusOK
		}
		writeJSON(w, status, postMessageResponse{MessageID: finalMessageID.String()})
	}
}

var errInstanceTerminated = errors.New("instance has terminated; no further messages accepted")

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
			Type:       q.Get("type"),
			SenderKind: q.Get("sender_kind"),
		}
		if s := q.Get("frame_id"); s != "" {
			frameID, err := uuid.Parse(s)
			if err != nil {
				badRequest(w, "invalid frame_id")
				return
			}
			u := shared.UUID(frameID)
			filter.FrameID = &u
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
