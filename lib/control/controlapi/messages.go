// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// messages.go — F1, F2. Unified message-layer endpoints.
//
// Plus the 2026-05-17 publisher-protocol unification
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
	Type                    string          `json:"type"`
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

// errPublisherSubscriptionNotLive is the sentinel returned when a
// publisher-side request fails the capability check: the named
// publisher-subscription is missing, stopped, failed, or bound to a
// different instance. "Live" = active OR still mounting — a fast
// publisher may emit before the reconciler records the mounting→active
// flip, and rejecting that window would drop a legitimate observation.
// Mapped to 403 Forbidden.
var errPublisherSubscriptionNotLive = errors.New("publisher-subscription not live (active or mounting) for this instance")

// unknownMessageTypeError is the sentinel returned when a posted
// message's `type:` is not declared in the instance's template's
// `messages:` registry. The handler maps it to HTTP 400 with a body
// that names both the rejected type and the declared set, so authors
// see exactly what the template admits. Refusing loudly at receipt is
// load-bearing for the message-schema story (`concept:message-schema`):
// the cheaper shape "accept and silently dead-letter" leaves authors
// guessing why their message never fired anything. Carries the rejected
// type and the declared set so the handler builds the response body
// without re-fetching the template.
//
// @concept: message-schema
type unknownMessageTypeError struct {
	Type     string
	Declared []string
}

func (e *unknownMessageTypeError) Error() string {
	return fmt.Sprintf("unknown message type %q (declared types: %v)", e.Type, e.Declared)
}

// reservedPayloadFieldWakeNodeIDs is the runtime-internal payload field
// that runtime-synthetic envelopes (`node/reset`, `instance/root`,
// `asset/materialize`) use to enumerate the node UUIDs to stale-mark in
// the promotion tx (see `lib/graph/frame/engine.go::advanceOneFrame`).
// An author-declared envelope MUST NOT carry this field on its payload
// — otherwise an operator with `message:send` permission could smuggle
// stale-mark targets through a declared message type and obtain a
// backdoor unconditional stale-mark against any node UUID they can
// name. The structural-divide @blessed-invariant in
// `lib/graph/frame/engine.go` notes that author-declared envelopes
// ship without `wake_node_ids` "only by accident"; this gate makes
// that property structural rather than accidental.
const reservedPayloadFieldWakeNodeIDs = "wake_node_ids"

// errPayloadCarriesReservedField is the sentinel returned when an
// author-declared message's payload carries the reserved
// `wake_node_ids` field. Mapped to HTTP 400.
var errPayloadCarriesReservedField = errors.New("message payload must not carry reserved field \"wake_node_ids\" (runtime-internal wake mechanism)")

// validateReservedPayloadFields enforces the reserved-field guard on
// an author-declared message's payload at receipt: the runtime-internal
// `wake_node_ids` field MUST NOT appear on the wire. This is the
// privilege-escalation guard — an author-declared envelope cannot
// smuggle stale-mark targets through the runtime-synthetic wake
// mechanism.
//
// Per the spec `.ok-planner/specs/2026-06-14-message-schema-layer-design.md`
// (§"Receipt-time body shape is documentation only") and `concept:message-
// schema`, the body shape itself is documentation plus a registration-
// time check on substitution refs. The actual body bytes are validated
// only at the receiver's dispatch via the existing attribute-validation
// machinery (per `concept:attribute`). Receipt-time body-schema validation
// would be a third read site for the payload bytes, violating
// `@blessed-invariant: 21`. This helper therefore restricts itself to
// the reserved-field guard, whose privilege-escalation rationale is
// independent of the inertness invariant.
//
// An undeclared type never reaches this helper (the caller filters first).
func validateReservedPayloadFields(payload json.RawMessage) error {
	// @constraint: decode the payload only enough to test for the
	// reserved key. An empty payload is admitted; there is nothing to
	// check.
	if len(payload) == 0 {
		return nil
	}
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		// @constraint: per `@blessed-invariant: message-inertness` we
		// do not validate body shape at receipt; a non-object payload
		// (array, scalar, malformed JSON) is admitted here and surfaces
		// at the substitution leaf if a receiver tries to walk it. The
		// reserved-field guard cannot see it either way (there is no
		// top-level key to inspect), which is the correct floor: the
		// guard exists to keep an operator from smuggling a
		// `wake_node_ids` *property*; a non-object payload cannot do
		// that by construction.
		return nil
	}
	if _, ok := body[reservedPayloadFieldWakeNodeIDs]; ok {
		return errPayloadCarriesReservedField
	}
	return nil
}

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
		dec := json.NewDecoder(req.Body)
		// @constraint: `DisallowUnknownFields` makes retired envelope
		// fields fail loud at receipt rather than silently roundtrip as
		// dead data. Retired DSL surfaces are removed from the code
		// entirely; templates and requests using them fail through
		// normal validator paths. A bare decoder admits unknown keys,
		// defeating that pledge — a publisher still sending the dropped
		// `target` field would silently send dead bytes with no
		// detection signal. The hard rejection is the detection signal.
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			badRequest(w, "invalid JSON body: "+err.Error())
			return
		}
		if body.Type == "" {
			badRequest(w, "type is required")
			return
		}
		// @constraint: the accepted `type:` set is gated on the instance's
		// template's declared `messages:` registry. The lookup runs INSIDE
		// the tx below — it needs the instance row to find the template
		// hash — but BEFORE the idempotency dedup INSERT and BEFORE the
		// envelope insert so an undeclared type can never silently
		// dead-letter or pollute the idempotency ledger.
		// @constraint: sender kind defaults to "operator" for back-compat. Publisher
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
		// @constraint: `sender` defaults to "operator" for operator-side requests;
		// publisher-side requests overwrite it with the publisher-
		// subscription's publisher_name (derived inside the tx below).
		// V1 supplies "operator" because cross-instance senders are V2;
		// the body's `sender` is ignored for trust until then.
		sender := "operator"
		// @constraint: idempotency-Key is MANDATORY on every emit: replay-dedup is a
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
		// @constraint: senderSubject discriminates the dedup tuple by requester so two
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
			// @constraint: message-type registry gate. Fetch the instance's
			// template and confirm body.Type is in the declared
			// `messages:` registry. This runs BEFORE the publisher
			// capability check (which could still reject 403), BEFORE
			// the idempotency dedup INSERT, and BEFORE the envelope
			// insert — so an undeclared type returns 400 without
			// polluting the idempotency ledger or persisting an
			// envelope no subscriber will ever match. A template with no
			// `messages:` block accepts no message type (the registry is
			// empty); the response names the empty set explicitly so the
			// diagnostic is self-evident.
			//
			// @concept: message-schema
			tpl, err := deps.Persist.Templates().GetByHash(ctx, inst.TemplateHash, tx)
			if err != nil {
				return err
			}
			if tpl == nil {
				// @constraint: a live instance pointing at a missing
				// template is a platform-internal invariant violation,
				// not an author-visible error. Surface as a generic
				// write error rather than silently passing the gate.
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
			if !matched {
				sort.Strings(declared)
				return &unknownMessageTypeError{Type: body.Type, Declared: declared}
			}
			// @constraint: reserved-field guard. Runs INSIDE the tx (read
			// in lockstep with the matched type) but BEFORE the
			// idempotency insert + envelope insert so a
			// runtime-internal-field-smuggling payload can never pollute
			// the idempotency ledger nor land an envelope no receiver
			// should ever match. This is the privilege-escalation guard:
			// the payload must not carry the runtime-internal
			// `wake_node_ids` field. Otherwise an operator with
			// `message:send` could obtain a backdoor unconditional
			// stale-mark against any node UUID. Body-schema validation
			// does NOT run here — per `@blessed-invariant: message-inertness`
			// and `concept:message-schema`, the declared body_schema is
			// documentation plus a registration-time check on
			// substitution refs; the actual body bytes are validated only
			// at the receiver's dispatch via the existing
			// attribute-validation gate. Adding a receipt-time validation
			// pass would be a third read site for payload bytes.
			//
			// @concept: message-schema
			if err := validateReservedPayloadFields(body.Payload); err != nil {
				return err
			}
			// @constraint: publisher capability check: the publisher-subscription must
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
				// @constraint: publisher path: `sender = publisher_name` already gives
				// per-publisher isolation, so the senderSubject column
				// stays empty.
				senderSubject = ""
			}
			// @constraint: dry-run: every validation step a real call would run
			// has now completed (instance exists, not terminated,
			// publisher capability gate passed). Skip the
			// idempotency-key insert and the message envelope insert
			// so the dry-run is side-effect-free.
			if isDryRun {
				return errDryRunOK
			}
			// @constraint: idempotency dedup: INSERT or lookup the dedup tuple BEFORE
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
				Type:       body.Type,
				Sender:     sender,
				SenderKind: senderKind,
				Payload:    body.Payload,
			}
			if err := runtime.EnqueueMessage(ctx, tx, deps.Persist.Messages(), enqueueReq); err != nil {
				return err
			}
			// @constraint: seed a frame so the message is actually delivered.
			// Messages are delivered ONLY into a running frame
			// (SweepDeliverMessagesForRunningFrames); a message POSTed to
			// a quiescent instance (no running frame) would otherwise
			// stay pending forever and never wake the subscribing node.
			// The emit path therefore enqueues a frame in the SAME tx as
			// the message insert (atomic: a crash mid-flow cannot leave a
			// pending message with no frame to carry it, nor a frame with
			// no message). The typed-message schema layer retires the
			// per-frame source-node-list; the inserted envelope IS the
			// frame's triggering message
			// (`col:rimsky_frames.triggering_message_id` FK), which is
			// what the cascade-graph observability endpoint and the
			// frame-origin audit story read.
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
				// @constraint: HTTP 400 with the rejected type AND the
				// declared registry set, so the operator can see exactly
				// what the template admits. The empty `declared_types`
				// case (a template with no `messages:` block) is
				// surfaced as an empty JSON array — the slice is
				// initialized non-nil so json.Marshal emits `[]`, not
				// `null`, even when zero types are declared.
				declared := unknownType.Declared
				if declared == nil {
					declared = []string{}
				}
				writeJSON(w, http.StatusBadRequest, map[string]any{
					"error":          "unknown message type",
					"type":           unknownType.Type,
					"declared_types": declared,
				})
				return
			}
			if errors.Is(err, errPayloadCarriesReservedField) {
				// @constraint: HTTP 400. Reserved-field
				// privilege-escalation guard: payloads on
				// author-declared types cannot carry `wake_node_ids`
				// (the runtime-synthetic wake field).
				writeJSON(w, http.StatusBadRequest, map[string]any{
					"error":          "reserved payload field",
					"reserved_field": reservedPayloadFieldWakeNodeIDs,
				})
				return
			}
			writeError(w, err)
			return
		}
		status := http.StatusCreated
		if replayed {
			// @constraint: replay path: returning the original message_id with
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

// handleListInstanceMessages is GET /instances/{id}/messages.
//
// Query params: `type`, `sender_kind`, `frame_id`, `delivered_after`,
// `delivered_before`, `limit`, `cursor`. Each filter is optional; all
// share AND semantics. The retired `kind` and `target` params have no
// column to filter against and are silently ignored by the URL parser.
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
		// @constraint: frame_id narrows to the messages delivered into a
		// given frame — the "what landed in frame X" forensic query for
		// fan-out debugging. Backed by the frame_id predicate in both
		// drivers' List.
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

// @constraint: handleGetMessage is GET /messages/{id}.
//
// @blessed-invariant: message-inertness — messages are inert in rimsky.
// The persistence-layer fetch here is one of a small fixed set of
// sanctioned read sites for message payload bytes — the others are the
// substitution-leaf walks in
// `code:lib/graph/attribute/substitution.go` (`resolveTriggerValue` and
// `resolveMessagesValue`), the cascade walker's `messagePayloadAsMap`
// decode used to populate the message-virtual-node settle signal's
// `attributes_delta` so subscriber CEL `when:` predicates can match
// against body fields
// (`code:lib/runtime/message_delivery.go::messagePayloadAsMap`), and the
// scheduler's `advanceOneFrame` runtime-internal wake-field extraction
// (`code:lib/graph/frame/engine.go::advanceOneFrame`), which pulls the
// rimsky-synthesized `wake_node_ids` array from the triggering message's
// payload inside the promotion tx. Rimsky never logs, formats with
// `%v`, validates beyond schema gates, transforms, or includes payload
// bytes in error messages. Same opacity discipline as
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
