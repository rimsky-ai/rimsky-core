// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: message
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
	nodepkg "github.com/rimsky-ai/rimsky-core/lib/graph/node"
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

func dedupSenderKind(senderKind string, ident auth.Identity) string {
	if senderKind == "publisher" {
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

type listMessagesResponse struct {
	Messages   []messageItem `json:"messages"`
	NextCursor string        `json:"next_cursor,omitempty"`
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
		senderKind := "operator"
		if body.PublisherSubscriptionID != "" {
			senderKind = "publisher"
		}
		sender := "operator"
		idempotencyKey := strings.TrimSpace(req.Header.Get("Idempotency-Key"))
		if idempotencyKey == "" {
			badRequest(w, "Idempotency-Key header is required")
			return
		}

		isDryRun := ModeFromContext(req.Context()) == auth.ModeDryRun
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
			// @concept: message-schema
			tpl, err := deps.Persist.Templates().GetByHash(ctx, inst.TemplateHash, tx)
			if err != nil {
				return err
			}
			if tpl == nil {
				return fmt.Errorf("instance %s template %s not found", instUUID, inst.TemplateHash)
			}
			// @decision: empty-message-as-root-trigger
			// @story: empty-message-wakes-roots
			declared := make([]string, 0, len(tpl.Spec.Messages)+1)
			declared = append(declared, "")
			for _, m := range tpl.Spec.Messages {
				declared = append(declared, m.Type)
			}
			matched := false
			for _, d := range declared {
				if d == body.Type {
					matched = true
					break
				}
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
				senderSubject = row.ID.String()
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
			if inst.TerminatedAt != nil {
				return errInstanceTerminated
			}
			if isDryRun {
				return errDryRunOK
			}
			enqueueReq := persistence.EnqueueMessageRequest{
				ID:         msgID,
				InstanceID: instUUID,
				Type:       body.Type,
				Sender:     sender,
				SenderKind: senderKind,
				Payload:    body.Payload,
			}
			if err := runtime.EnqueueMessage(ctx, tx, deps.Persist, enqueueReq); err != nil {
				return err
			}
			return nil
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
			var schemaViolation *nodepkg.MessageBodySchemaViolation
			if errors.As(err, &schemaViolation) {
				writeJSON(w, http.StatusBadRequest, map[string]any{
					"error": schemaViolation.Error(),
					"type":  schemaViolation.Type,
				})
				return
			}
			var unknownType *unknownMessageTypeError
			if errors.As(err, &unknownType) {
				declared := make([]string, 0, len(unknownType.Declared))
				for _, d := range unknownType.Declared {
					if d == "" {
						continue
					}
					declared = append(declared, d)
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
			Sender:     q.Get("sender"),
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
		if s := q.Get("pending"); s != "" {
			switch s {
			case "true", "false":
				pending := s == "true"
				filter.Pending = &pending
			default:
				badRequest(w, "invalid pending (true or false required)")
				return
			}
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
		writeJSON(w, http.StatusOK, listMessagesResponse{Messages: items, NextCursor: page.NextCursor})
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
