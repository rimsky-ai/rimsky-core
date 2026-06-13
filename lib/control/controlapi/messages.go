// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// messages.go — F1, F2. Unified message-layer endpoints.
//
// Spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Messages / Control-api endpoints.
//
// Plus the 2026-05-17 publisher-protocol unification
// (.ok-planner/specs/2026-05-17-sensor-messaging-unification-design.md):
//
//   - POST /instances/{id}/messages accepts `sender_kind: "publisher"`
//     with a `publisher_subscription_id` capability token, capability-
//     checked against `rimsky_publisher_subscriptions`.
//   - The `Idempotency-Key` HTTP header drives universal dedup via the
//     `rimsky_message_idempotencies` table. Replays return the original
//     message_id with 200 OK rather than inserting a duplicate envelope.
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

// senderSubjectAnonymous is the sentinel `sender_subject` value for an
// operator-side request served under anonymous-mode (no api-key). Pinned
// here rather than at the call site so a future plumbing change can't
// accidentally drift the column value (a drift would silently revive
// the cross-tenant collision the SenderSubject column exists to
// prevent).
const senderSubjectAnonymous = "anonymous"

// operatorSenderSubject computes the `sender_subject` column value for
// an operator-side message-create — the per-caller discriminator that
// makes the idempotency dedup tuple resistant to cross-tenant collisions.
// Returns the api-key UUID string for an authenticated request, the
// anonymous sentinel for an anonymous-mode request, and the empty
// string when no identity is on context (route-only test harness; the
// production gate always installs an identity).
func operatorSenderSubject(ident auth.Identity) string {
	if ident.KeyID != nil {
		return ident.KeyID.String()
	}
	if ident.Kind == auth.IdentityAnonymous {
		return senderSubjectAnonymous
	}
	return ""
}

// dedupSenderKind computes the `sender_kind` column value for the
// idempotency dedup tuple — the structural source-of-claim
// discriminator that namespaces the `sender` string so a publisher
// whose operator-chosen publisher_name happens to be the literal
// `"operator"` cannot collide with operator-side emits on the same
// instance + Idempotency-Key. The wire-level senderKind ("operator" /
// "publisher") is the primary signal; an operator-side request in
// implicit anonymous mode is bucketed separately as "anonymous" so
// rotating anonymous → authenticated also rolls dedup tuples (the
// anonymous floor and a future bootstrap admin should not share a
// dedup tuple).
func dedupSenderKind(wireSenderKind string, ident auth.Identity) string {
	if wireSenderKind == "publisher" {
		return "publisher"
	}
	if ident.Kind == auth.IdentityAnonymous {
		return "anonymous"
	}
	return "operator"
}

// registerMessagesRoutes wires the message endpoints.
func registerMessagesRoutes(r chi.Router, deps AppDeps) {
	r.Post("/instances/{id}/messages", gate(deps, "message:send", handleCreateMessage(deps)))
	r.Get("/instances/{id}/messages", gate(deps, "message:read", handleListInstanceMessages(deps)))
	r.Get("/messages/{id}", gate(deps, "message:read", handleGetMessage(deps)))
}

// postMessageRequest is the body shape of POST /instances/{id}/messages.
//
// Per spec §Messages / Envelope, `sender` is derived from caller
// identity. Operator-side callers default to sender="operator",
// sender_kind="operator". Publisher-side callers (bundled sensors)
// pass sender_kind="publisher" + publisher_subscription_id; rimsky
// derives `sender` from the publisher-subscription row's
// publisher_name (operator-supplied `sender` is ignored for trust).
type postMessageRequest struct {
	Kind                    string          `json:"kind"`
	Target                  string          `json:"target,omitempty"`
	Payload                 json.RawMessage `json:"payload,omitempty"`
	Sender                  string          `json:"sender,omitempty"`
	SenderKind              string          `json:"sender_kind,omitempty"`
	PublisherSubscriptionID string          `json:"publisher_subscription_id,omitempty"`
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

// errPublisherSubscriptionNotLive is the sentinel returned when a
// publisher-side request fails the capability check: the named
// publisher-subscription is missing, stopped, failed, or bound to a
// different instance. "Live" = active OR still mounting — a fast
// publisher may emit before the reconciler records the mounting→active
// flip, and rejecting that window would drop a legitimate observation.
// Mapped to 403 Forbidden.
var errPublisherSubscriptionNotLive = errors.New("publisher-subscription not live (active or mounting) for this instance")

// handleCreateMessage is POST /instances/{id}/messages.
//
// Validates the body, requires the mandatory Idempotency-Key header,
// ensures the instance exists and is not terminated, enforces the
// sender-kind capability check for publisher requests, applies
// idempotency dedup, then enqueues via runtime.EnqueueMessage. Returns
// the message id (201 on first insert, 200 on replay).
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
		// Sender kind defaults to "operator" for back-compat. Publisher
		// senders explicitly set "publisher" + publisher_subscription_id.
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
		// `sender` defaults to "operator" for operator-side requests;
		// publisher-side requests overwrite it with the publisher-
		// subscription's publisher_name (derived inside the tx below).
		// V1 supplies "operator" because cross-instance senders are V2;
		// the body's `sender` is ignored for trust until then.
		sender := "operator"
		// Idempotency-Key is MANDATORY on every emit: replay-dedup is a
		// platform guarantee, not an opt-in. A missing key can never
		// silently bypass dedup, so reject keyless requests at the
		// boundary (request-level 400, pre-tx — alongside the other
		// request-level validations). This guard runs ahead of BOTH the
		// dry-run branch and the dedup INSERT below: a dry-run preview of
		// an emit must still carry the key it would dedup on, so a keyless
		// dry-run is rejected too.
		idempotencyKey := strings.TrimSpace(req.Header.Get("Idempotency-Key"))
		if idempotencyKey == "" {
			badRequest(w, "Idempotency-Key header is required")
			return
		}

		isDryRun := ModeFromContext(req.Context()) == authModeDryRun
		msgID := shared.UUID(uuid.New())
		instUUID := shared.UUID(instanceID)
		// senderSubject discriminates the dedup tuple by requester so two
		// distinct api-keys posting to the same instance with the same
		// Idempotency-Key can no longer cross-collide (the second caller
		// would otherwise receive the first caller's message_id back as
		// a "replay" even though their payloads differ; the second caller
		// could also probe whether the first used a given key by sending
		// it). Operator with api-key → the api-key UUID; operator
		// anonymous-mode → "anonymous"; publisher → "" (the `sender`
		// column already carries the per-publisher publisher_name and
		// provides isolation). senderSubject is rewritten alongside
		// `sender` for the publisher capability path below.
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
			// Publisher capability check: the publisher-subscription must
			// be live (active, or still mounting) and bound to THIS
			// instance. We look up the row by id, verify the state and
			// that instance_id matches, then derive `sender` from the
			// row's publisher_name. Mounting is accepted because the
			// reconciler flips mounting→active only AFTER the publisher's
			// Subscribe RPC returns — a fast publisher can emit its first
			// message in that window, and rejecting it would drop a
			// legitimate observation (no-message-loss over the stricter
			// gate). failed/stopped rows are still rejected.
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
				// Publisher path: `sender = publisher_name` already gives
				// per-publisher isolation, so the senderSubject column
				// stays empty.
				senderSubject = ""
			}
			// Dry-run: every validation step a real call would run
			// has now completed (instance exists, not terminated,
			// publisher capability gate passed). Skip the
			// idempotency-key insert and the message envelope insert
			// so the dry-run is side-effect-free.
			if isDryRun {
				return errDryRunOK
			}
			// Idempotency dedup: INSERT or lookup the dedup tuple BEFORE
			// inserting the message envelope. The Idempotency-Key is
			// mandatory (guarded request-level above), so this always
			// runs. On conflict, return the previously-recorded message_id
			// and skip the envelope insert. Wrap in the same tx so a crash
			// mid-flow doesn't leave a dedup row pointing at a
			// never-inserted message.
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
				Kind:       body.Kind,
				Sender:     sender,
				SenderKind: senderKind,
				Target:     body.Target,
				Payload:    body.Payload,
			}
			if err := runtime.EnqueueMessage(ctx, tx, deps.Persist.Messages(), enqueueReq); err != nil {
				return err
			}
			// Seed a frame so the message is actually delivered. Messages
			// are delivered ONLY into a running frame
			// (SweepDeliverMessagesForRunningFrames); a message POSTed to a
			// quiescent instance (no running frame) would otherwise stay
			// pending forever and never wake the subscribing node. The
			// emit path therefore enqueues/coalesces a frame in the SAME tx
			// as the message insert (atomic: a crash mid-flow can't leave a
			// pending message with no frame to carry it, nor a frame with no
			// message). The scheduler's frame engine promotes the queued
			// frame to running on the next tick and the delivery sweep
			// delivers the pending message into it, firing the cascade.
			//
			// Frame source: the target node (resolved by type). For a
			// broadcast envelope (empty target) any node in the instance is
			// a valid source — the frame source only identifies the
			// triggering node; the delivery sweep delivers every pending
			// message regardless of which node seeded the frame.
			sourceNodeID, ok, srcErr := resolveMessageFrameSource(ctx, deps.Persist, tx, instUUID, body.Target)
			if srcErr != nil {
				return srcErr
			}
			if !ok {
				// No node to source a frame on (instance has no nodes) —
				// nothing to deliver to; leave the message pending. This
				// is degenerate (a template with zero nodes) and not worth
				// failing the emit over.
				return nil
			}
			_, frErr := frame.EnqueueOrCoalesce(ctx, deps.Persist, tx, instUUID, sourceNodeID)
			return frErr
		})
		if isDryRun && errors.Is(err, errDryRunOK) {
			WriteDryRunResponseForced(w, "would_have_sent", map[string]any{
				"instance_id":  instanceID.String(),
				"message_kind": body.Kind,
				"sender_kind":  senderKind,
				"target":       body.Target,
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
			if errors.Is(err, errMessageTargetUnknown) {
				badRequest(w, err.Error())
				return
			}
			writeError(w, err)
			return
		}
		status := http.StatusCreated
		if replayed {
			// Replay path: returning the original message_id with
			// 200 OK signals idempotent dedup. The body shape is
			// identical so caller code can stay generic.
			status = http.StatusOK
		}
		writeJSON(w, status, postMessageResponse{MessageID: finalMessageID.String()})
	}
}

// errInstanceTerminated is the sentinel returned when the message
// target instance has already terminated. Mapped to 409 Conflict.
var errInstanceTerminated = errors.New("instance has terminated; no further messages accepted")

// errMessageTargetUnknown is the sentinel returned when the message's
// `target` names a node type that doesn't exist in the instance. Mapped
// to 400 Bad Request: an operator typo MUST surface as a precise error
// rather than silently fanning out broadcast-style (which would consume
// frame slots and risk starving other deliveries to nodes that have no
// subscription to the message). Empty target stays broadcast.
var errMessageTargetUnknown = errors.New("target names no node type in this instance")

// resolveMessageFrameSource picks the node to source a delivery frame on
// for a just-enqueued message. When target names a node type, the
// matching node is the source. For a broadcast envelope (empty target)
// the first node in the instance is used — the frame source only marks
// the triggering node; the delivery sweep delivers every pending message
// into the running frame regardless of which node seeded it. Returns
// (zero, false, nil) when the instance has no nodes. A non-empty target
// that names no existing node type returns errMessageTargetUnknown so
// the handler can surface a 400 — distinguishing "intended broadcast"
// (empty target) from "typo target" (non-empty but missing).
func resolveMessageFrameSource(
	ctx context.Context, persist persistence.Tables, tx persistence.Tx,
	instanceID shared.UUID, target string,
) (shared.UUID, bool, error) {
	nodes, err := persist.Nodes().ListByInstance(ctx, instanceID, tx)
	if err != nil {
		return shared.UUID{}, false, err
	}
	if len(nodes) == 0 {
		return shared.UUID{}, false, nil
	}
	if target != "" {
		for _, n := range nodes {
			if n.NodeType == target {
				return n.ID, true, nil
			}
		}
		// Target names a node type that doesn't exist in this instance —
		// surface as a 400 rather than silently broadcasting. An operator
		// typo must NOT fan out to nodes that have no subscription to the
		// message (which would consume frame slots and risk starving other
		// deliveries). Empty target stays broadcast.
		return shared.UUID{}, false, errMessageTargetUnknown
	}
	return nodes[0].ID, true, nil
}

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
		// frame_id narrows to the messages delivered into a given frame —
		// the "what landed in frame X" forensic query for backfill / fan-out
		// debugging. Backed by the frame_id predicate in both drivers' List.
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

// handleGetMessage is GET /messages/{id}.
//
// @blessed-invariant: messages are inert in rimsky. The persistence-
// layer fetch here is one of two sanctioned read sites for message
// payload bytes (the other is the substitution-leaf walk in
// graph/attribute/substitution.go::resolveTrigger). Rimsky never logs,
// formats with `%v`, validates beyond schema gates, transforms, or
// includes payload bytes in error messages. Same opacity discipline as
// `@blessed-invariant 20/21`.
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
