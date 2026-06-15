// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// message_delivery.go — message queue + delivery at frame boundary.
//
// Messages are typed envelopes that drive instance frame creation; operators,
// publishers (bundled sensors), and instance-internal emit-nodes all emit
// messages via the generic `POST /instances/{id}/messages` endpoint or the
// in-process EnqueueMessage helper. At frame creation the scheduler delivers
// exactly one pending message per invocation; the just-delivered envelope
// settles its virtual node-type (the type-path itself names a virtual node
// in the subscription graph) with `terminal/success`, and receivers that
// subscribe via `node: <message-type>, type: terminal/success` stale-mark
// through the same edge map the rest of the cascade uses.
//
// @concept: message
// @concept: frame
//
// @constraint: one-message-per-frame is the only delivery shape under the
// message-schema-layer redesign — the cheaper shape "deliver everything
// pending and let downstream sort it out" silently collapses distinct
// override messages into one rerun and is forbidden.
//
// @blessed-invariant: message-inertness — messages are inert in rimsky. The
// delivery path touches envelope routing fields (type, sender, sender_kind,
// frame_id, delivered_at) but never the `payload` bytes. The sanctioned
// read sites for payload bytes live at a small fixed set of locations:
//   - the substitution leaf in
//     `code:graph/attribute/substitution.go::resolveTriggerValue` (the
//     `trigger.message.payload.<field>` arm, walks payload via
//     `walkPath`);
//   - the parallel `messages.<type>.<field>` arm in
//     `code:graph/attribute/substitution.go::resolveMessagesValue`;
//   - the cascade walker's `messagePayloadAsMap` decode below, used to
//     populate the message-virtual-node settle signal's
//     `attributes_delta` so subscriber CEL `when:` predicates can match
//     against body fields;
//   - the persistence-layer fetch in
//     `code:control/controlapi/messages.go::handleGetMessage` (which
//     surfaces the row verbatim to the operator); and
//   - the scheduler's `advanceOneFrame` runtime-internal wake-field
//     extraction (`code:graph/frame/engine.go::advanceOneFrame`), which
//     pulls the rimsky-synthesized `wake_node_ids` array from the
//     triggering message's payload inside the promotion tx — distinct
//     surface from the four body-reading sites because the field is
//     rimsky-owned and runtime-synthetic, not a user-authored body
//     shape.
//
// Same opacity discipline as the inert-payload invariants on other
// envelope-shaped rows.

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/cascade"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	signalaudit "github.com/rimsky-ai/rimsky-core/lib/foundation/signal/audit"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

// EnqueueMessage inserts a message envelope into rimsky_messages with
// delivered_at NULL. Validates Type / SenderKind. Caller passes a
// pre-allocated ID so the message id can flow back to the operator on
// `POST /instances/{id}/messages`.
func EnqueueMessage(ctx context.Context, tx persistence.Tx, m persistence.MessagesTable, req persistence.EnqueueMessageRequest) error {
	if req.ID == (shared.UUID{}) {
		return errors.New("EnqueueMessage: id required")
	}
	if req.InstanceID == (shared.UUID{}) {
		return errors.New("EnqueueMessage: instance_id required")
	}
	if req.Type == "" {
		return errors.New("EnqueueMessage: type required")
	}
	if req.Sender == "" {
		return errors.New("EnqueueMessage: sender required")
	}
	switch req.SenderKind {
	case "operator", "publisher", "instance":
	default:
		return fmt.Errorf("EnqueueMessage: unknown sender_kind %q (want operator|publisher|instance)", req.SenderKind)
	}
	if req.ReceivedAt.IsZero() {
		req.ReceivedAt = time.Now().UTC()
	}
	return m.Insert(ctx, tx, req)
}

// DeliveredMessages is the return shape of DeliverPendingMessages —
// the messages that were stamped delivered_at + frame_id in this call.
// Callers (the frame-boundary path) use this to drive cascade /
// stale-mark walks for subscriber resolution.
type DeliveredMessages struct {
	Messages []persistence.MessageRow
}

// SweepDeliverMessagesForRunningFrames is the frame-boundary sweep that
// the scheduler invokes after `graph/frame.RunTick` promotes queued
// frames to running. For each running frame whose owning instance still
// has pending messages, the sweep calls `DeliverPendingMessages` inside
// its own short tx.
//
// Idempotent: a message that's already been marked delivered_at + frame_id
// is filtered out by `MessagesTable.ListPendingForInstance`, so re-running
// the sweep per tick is safe. Per-frame work is bounded by the number of
// pending messages — if no messages are pending for a given instance the
// helper returns immediately.
//
// @constraint: the sweep paginates `FramesTable.ListForObservability`
// rather than introducing a dedicated "running frames" index — running
// frames are bounded by the per-instance "at most one running frame"
// invariant + the number of active instances, so a single page batch is
// sufficient for v1 throughput.
func SweepDeliverMessagesForRunningFrames(
	ctx context.Context, persist persistence.Tables, queue persistence.Queue, logger shared.Logger, now time.Time,
) error {
	if persist == nil {
		return nil
	}
	pag := persistence.ListPagination{Limit: 256}
	for {
		var page persistence.PaginatedListResult[persistence.FrameRow]
		if err := persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			p, err := persist.Frames().ListForObservability(ctx,
				persistence.FrameListFilter{State: persistence.FrameStateRunning},
				pag, tx)
			page = p
			return err
		}); err != nil {
			return fmt.Errorf("SweepDeliverMessagesForRunningFrames: list: %w", err)
		}
		for _, f := range page.Rows {
			if err := deliverForRunningFrame(ctx, persist, queue, f.InstanceID, f.FrameID, now); err != nil {
				if logger != nil {
					logger.Warn("SweepDeliverMessagesForRunningFrames: deliver failed",
						"frame_id", f.FrameID.String(),
						"instance_id", f.InstanceID.String(),
						"error", err.Error())
				}
				continue
			}
		}
		if page.NextCursor == "" {
			return nil
		}
		pag.Cursor = page.NextCursor
	}
}

// deliverForRunningFrame delivers exactly one pending message into the
// running frame (one-message-per-frame).
//
// After marking the message delivered, walks the per-template subscription
// edges keyed on the message-virtual-node sender-type and stale-marks every
// receiver node whose subscription matches `terminal/success` on that
// virtual node-type.
func deliverForRunningFrame(
	ctx context.Context, persist persistence.Tables, queue persistence.Queue,
	instanceID, frameID shared.UUID, now time.Time,
) error {
	return persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		inst, err := persist.Instances().Get(ctx, instanceID, tx)
		if err != nil {
			return fmt.Errorf("get instance: %w", err)
		}
		if inst == nil {
			return nil
		}
		delivered, err := DeliverPendingMessages(ctx, tx, persist.Messages(),
			instanceID, frameID, now)
		if err != nil {
			return err
		}
		if len(delivered.Messages) == 0 {
			return nil
		}
		// @constraint: emit one terminal/success message-virtual-node settle
		// signal per delivered envelope, built once via
		// messageVirtualNodeSettleSignal so the audit-log emission and the
		// downstream cascade-walker match input are the same struct (a
		// divergent payload shape between the two surfaces would make
		// subscribers' CEL `when:` predicates and audit rows disagree).
		signalsByMessageID := make(map[shared.UUID]signalpkg.Signal, len(delivered.Messages))
		for _, msg := range delivered.Messages {
			msgSig := messageVirtualNodeSettleSignal(msg)
			signalsByMessageID[msg.ID] = msgSig
			if err := signalaudit.EmitSignal(ctx, persist.Events(),
				instanceID, shared.UUID{}, msgSig, now, tx); err != nil {
				return fmt.Errorf("emit message signal: %w", err)
			}
		}
		return cascadeMessageVirtualNodeSettleInTx(ctx, persist, queue, tx,
			instanceID, frameID, delivered.Messages, signalsByMessageID, inst.TemplateHash,
			inst.MainRunScopeID)
	})
}

// messageVirtualNodeSettleSignal builds the canonical terminal/success
// signal that represents the just-delivered message envelope as a
// virtual-node settle. The shape parallels a real node's terminal/success
// payload (`changed` / `attributes_delta` / `change_summary`) so
// downstream substitution and CEL `when:` predicates see body fields by
// name. Single source of construction so the audit emit and the cascade
// match input cannot drift.
//
// @concept: message
// @concept: signal
func messageVirtualNodeSettleSignal(msg persistence.MessageRow) signalpkg.Signal {
	return signalpkg.Signal{
		Type: signalpkg.TypePath("terminal/success"),
		Payload: map[string]any{
			"changed":          true,
			"attributes_delta": messagePayloadAsMap(msg.Payload),
			"change_summary":   "message-virtual-node:" + msg.Type,
		},
	}
}

// messagePayloadAsMap decodes the message envelope's payload bytes
// into a map[string]any when JSON-shaped; falls back to a stub map
// carrying byte length. Per the message-inertness invariant the bytes
// are not transformed for any other purpose — this shape exists only
// so subscriber CEL `when:` predicates can read into
// `payload.attributes_delta.foo` at evaluation time (the signal Payload
// places the decoded body under the `attributes_delta` key, matching
// what `code:graph/node/template_validator.go::checkAttributesDeltaFields`
// validates against).
//
// @concept: message
func messagePayloadAsMap(payload []byte) map[string]any {
	if len(payload) == 0 {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(payload, &out); err == nil && out != nil {
		return out
	}
	return map[string]any{"_raw_bytes": len(payload)}
}

// cascadeMessageVirtualNodeSettleInTx walks the standard subscription-
// edge map for each just-delivered message, treating the message's
// `type` as the sender node-type and `terminal/success` as the emitted
// signal. Subscribers declared via `node: <message-type>, type:
// terminal/success` match through the same machinery that gates every
// other node→node cascade.
//
// Runs inside the caller's tx so the stale-mark commits atomically with
// the MarkDelivered write — without this gating in the same tx, a
// re-tick that arrived between MarkDelivered and stale-mark would treat
// the message as already delivered and never wake the receivers.
//
// Parked vs running receivers: the parked branch mirrors the standard
// runner_terminal.go cascade walker — a parked receiver is woken via
// wakeParkedReceiverWithDepsInTx (which transitions parked → stale,
// stamps frame_id, rebinds the run row, and writes the resume audit
// event), and a non-parked receiver stale-marks via the same
// MarkStaleForCascade call the standard walker uses. Sharing the
// parked-wake primitive across both cascade walkers prevents drift —
// a parked subscriber would otherwise be silently stale-marked here,
// the supervisor would never re-dispatch it, and a message arrival
// could fail to wake a receiver that was awaiting a callback or
// deadline.
//
// Wait-set rows are NOT inserted here: the upstream is a virtual
// node-type with no real run row, so a wait-set entry would have to
// carry a fabricated sender_run_id violating the FK constraint to
// rimsky_node_runs. Receivers stale-mark (or wake from parked) and
// proceed through the standard dispatch path; substitution from the
// message body flows through the {{messages.<type>.<field>}} grammar,
// which resolves against rimsky_frames.triggering_message_id rather
// than a wait-set drain.
//
// Asymmetry with the standard cascade walker (runner_terminal.go::
// cascadeSubscribersStaleInTx, which DOES insert wait-set rows): a
// receiver subscribed to BOTH a message-virtual-node AND a real upstream
// node carries one wait-set row gating it on the regular upstream and
// no wait-set row gating it on the message. The receiver becomes
// dispatchable as soon as the regular upstream's wait-set drains —
// which under the load-bearing ordering rule below is always AFTER the
// message has already delivered and substitution can resolve the body.
// The asymmetry is safe because of the ordering, not because both
// paths gate symmetrically.
//
// @constraint: deliver-before-walk — SweepDeliverMessagesForRunningFrames
// stamps delivered_at + frame_id on the triggering envelope BEFORE the
// next scheduler tick walks the runner-side cascade. This ordering is
// what lets the message-virtual-node settle skip wait-set insertion:
// by the time any receiver gated by a regular upstream becomes
// dispatchable, the triggering_message_id row already exists with
// delivered_at stamped, so attribute-substitution against
// {{messages.<type>.<field>}} resolves deterministically. The
// scheduler-tick sequence is frame-end-detect → advance-queued
// (promotes + wake_node_ids stale-marks) → message-delivery-sweep
// (stamps delivered_at + frame_id) → runner-tick (consumes wait-sets,
// dispatches stale rows). Anyone rewiring the tick order must preserve
// "deliver-before-walk" or fold the wait-set row in symmetrically.
//
// @constraint: per-origin delivery-latency profile (informational; the
// invariant above is what is load-bearing — this is the timing it
// produces):
//
//   - Operator-posted / publisher-posted messages enqueue both the
//     envelope and its delivering frame inside the request tx at
//     `controlapi.handleCreateMessage`. If the instance has no running
//     frame, the next scheduler tick's advance-queued step promotes
//     the just-queued frame, and that same tick's message-delivery
//     sweep stamps delivered_at on the envelope — single-tick latency.
//     If a frame is already running, the new frame stays queued until
//     the running one settles; latency is whatever the running frame
//     takes to finish, plus one tick.
//
//   - Cascade-emit messages (a message-emitter node settling at
//     `runner_emit_message.emitCascadeMessageInTx` inside
//     `applyTerminalComplete`) enqueue the envelope AND its delivering
//     frame inside the runner-tick that just ran. The frame is queued
//     AFTER this same tick's message-delivery sweep has already
//     completed for the previous frame, so the cascade-emit message
//     can only deliver on the NEXT tick at the earliest — minimum
//     one-tick latency. This is correct semantics (the emit-node's
//     own frame F0 is still running when the envelope inserts; the
//     new frame can't promote until F0 settles) and is structurally
//     required for STORY-cascade-emit + STORY-cross-frame-coupling.
//
//   - Runtime-synthetic envelopes (`node/invalidate`, `node/reset`,
//     `asset/materialize`, `instance/root`) enqueued via
//     `EnqueueSyntheticWakeFrame` follow the same pattern as cascade-
//     emit when triggered from inside a runner-tick (one-tick floor),
//     or the operator-/publisher-tick pattern when triggered from a
//     request tx (e.g. `controlapi/assets.go`'s asset-materialize
//     path).
//
// @constraint: a future scheduler change that moves message-delivery-
// sweep to BEFORE runner-tick (to shave operator-message latency) MUST
// preserve the cascade-emit one-tick floor — the emit's frame is
// queued mid-runner-tick and cannot be delivered into in the same
// tick without re-running the sweep after the runner-tick, breaking
// the "deliver-before-walk" property that lets the message-virtual-
// node settle skip wait-set insertion.
//
// @constraint: message-substitution-via-delivered-row —
// message-virtual-node-settle receivers gate via stale-mark +
// payload-substitution, not via wait-set drain. Substitution reads
// the message delivered into the frame via
// `Messages().ListDeliveredForFrame` (see
// `code:runtime/runner_acquire_helpers.go::triggerMessageForFrame` and
// `code:runtime/runner_dispatch.go::lookupTriggerMessageForFrame`).
// That row is by construction the same as
// `rimsky_frames.triggering_message_id` under the
// serial-queue-per-instance + same-tx enqueue invariants: the message-
// delivery sweep stamps `delivered_at + frame_id` on the envelope the
// frame's `triggering_message_id` already points at, and one-message-
// per-frame keeps the lists in lock-step. A reader debugging
// substitution behaviour should look at the delivered-message row, not
// the frame's triggering_message_id column; the two are equal but the
// queries hit different code paths.
//
// @concept: message
// @concept: cascade
// @concept: signal
func cascadeMessageVirtualNodeSettleInTx(
	ctx context.Context, persist persistence.Tables, queue persistence.Queue, tx persistence.Tx,
	instanceID, frameID shared.UUID, messages []persistence.MessageRow,
	signalsByMessageID map[shared.UUID]signalpkg.Signal,
	templateHash string,
	instanceMainRunScopeID shared.UUID,
) error {
	if len(messages) == 0 {
		return nil
	}
	tmpl, err := persist.Templates().GetByHash(ctx, templateHash, tx)
	if err != nil {
		return fmt.Errorf("cascadeMessageVirtualNodeSettleInTx: get template: %w", err)
	}
	if tmpl == nil {
		return nil
	}
	subRefs := node.ExtractSubstitutionRefsFromTemplate(tmpl.Spec)
	msgRefs := node.ExtractMessageRefsFromTemplate(tmpl.Spec)
	edges, err := node.BuildSubscriptionEdges(tmpl.Spec, subRefs, msgRefs)
	if err != nil {
		return fmt.Errorf("cascadeMessageVirtualNodeSettleInTx: build edges: %w", err)
	}
	if edges == nil {
		return nil
	}
	instNodes, err := persist.Nodes().ListByInstance(ctx, instanceID, tx)
	if err != nil {
		return fmt.Errorf("cascadeMessageVirtualNodeSettleInTx: list nodes: %w", err)
	}
	byType := make(map[string][]persistence.NodeRow, len(instNodes))
	for _, n := range instNodes {
		byType[n.NodeType] = append(byType[n.NodeType], n)
	}
	successType := signalpkg.TypePath("terminal/success")
	for _, msg := range messages {
		// @constraint: reuse the signal the caller already constructed
		// for the audit emit so a future change to the canonical
		// payload shape lands on both surfaces at once.
		msgSig, ok := signalsByMessageID[msg.ID]
		if !ok {
			msgSig = messageVirtualNodeSettleSignal(msg)
		}
		// @constraint: match against the message-virtual-node sender
		// key — the message's `type` IS the sender node-type in the
		// subscription graph. The standard match function handles exact
		// + trailing-wildcard patterns over `terminal/*`, so an author
		// who writes `subscribes: [{node: ping/recheck, type:
		// terminal/success}]` (or `terminal/*`) gets a hit here.
		matched := edges.Match(msg.Type, successType)
		for _, e := range matched {
			if e.WhenExpr != nil {
				// @deliberate: discard Eval's error return — the spec's
				// safe-navigation default surfaces CEL runtime errors as
				// `(false, nil)` with a slog warn; the error path is
				// reserved for future fatal-eval cases and stays
				// unreachable today.
				ok, _ := e.WhenExpr.Eval(msgSig)
				if !ok {
					continue
				}
			}
			receivers := byType[e.ReceiverNodeType]
			for _, r := range receivers {
				// @concept: cascade
				// @constraint: wake-up effects (affirm + mark-stale +
				// enqueue-frame) gate on wake_on_change. A subscription
				// with wake_on_change: false skips the wake-up path here —
				// the message is still routed via the signal-emit above
				// for audit, but the receiver is not stale-marked or
				// enqueued.
				//
				// @deliberate: message cascade has no wait-set surface
				// (the receiver reads from the delivered message envelope
				// itself, not from a sender node's attribute), so there
				// is no wait-set insert to preserve outside this gate.
				// The equivalent "still needs to read the sender's value"
				// property for messages is automatic: the message row
				// stays delivered_at/frame_id stamped and is readable via
				// the receiver's substitution context whenever the
				// receiver eventually dispatches via another edge.
				if !e.WakeOnChange {
					continue
				}
				// @deliberate: default to the instance's main RunScope
				// when the receiver has no in-flight scope projection —
				// message cascade is intra-scope on the main RunScope,
				// and AffirmNodeRunRow must insert a pending row keyed
				// on (receiver_node_id, scope) so MarkStaleForCascade
				// has a row to UPDATE. Reuses the caller's already-
				// loaded MainRunScopeID rather than re-fetching the
				// instance row per receiver — under one-message-per-
				// frame every delivered message would otherwise issue
				// K+1 instance reads (K receivers).
				var receiverScopeID shared.UUID
				if r.RunScopeID != nil {
					receiverScopeID = *r.RunScopeID
				} else {
					receiverScopeID = instanceMainRunScopeID
				}
				if err := persist.Nodes().AffirmNodeRunRow(ctx, r.ID, receiverScopeID, frameID, tx); err != nil {
					// @concept: run-scope — the walker MUST NOT cross
					// into closed RunScopes; a closed scope means the
					// receiver's parent rendezvous has fired. Skip this
					// receiver and continue the cascade walk so a benign
					// race with scope closure doesn't strand the rest of
					// the receivers.
					if errors.Is(err, persistence.ErrRunScopeClosed) {
						continue
					}
					return fmt.Errorf("cascadeMessageVirtualNodeSettleInTx: affirm receiver run %s: %w", r.ID, err)
				}
				runID, ok, err := queue.GetInFlightRunForNode(ctx, tx, r.ID, receiverScopeID)
				if err != nil {
					return fmt.Errorf("cascadeMessageVirtualNodeSettleInTx: resolve receiver run %s: %w", r.ID, err)
				}
				if !ok {
					continue
				}
				// @constraint: mirror the standard cascade walker's
				// parked-branch (runner_terminal.go::
				// cascadeSubscribersStaleInTx): a parked receiver wakes
				// via the shared primitive, a non-parked one stale-marks.
				// Without this split, a parked subscriber is silently
				// stale-marked but never re-dispatched and the message
				// arrival fails to wake it.
				if r.State == cascade.NodeStateParked {
					if err := wakeParkedReceiverWithDepsInTx(ctx, persist, queue, tx, r, frameID); err != nil {
						return fmt.Errorf("cascadeMessageVirtualNodeSettleInTx: wake parked %s: %w", r.ID, err)
					}
				} else {
					if err := persist.Nodes().MarkStaleForCascade(ctx, runID, frameID, tx); err != nil {
						return fmt.Errorf("cascadeMessageVirtualNodeSettleInTx: mark stale %s: %w", r.ID, err)
					}
				}
			}
		}
	}
	return nil
}

// DeliverPendingMessages selects pending messages for the instance, picks
// the oldest one, marks it delivered with the new frame_id, and returns
// the row so the caller can drive subscription matching.
//
// One-message-per-frame: at most one message is delivered per call; the
// rest remain pending until the next frame. The cheaper shape "deliver
// everything pending and let downstream sort it out" is forbidden — it
// silently collapses distinct override envelopes into one rerun. The
// load-bearing invariant is the single-row LIMIT 1 selection downstream
// of ListPendingForInstance.
func DeliverPendingMessages(
	ctx context.Context, tx persistence.Tx, m persistence.MessagesTable,
	instanceID shared.UUID, frameID shared.UUID, now time.Time,
) (DeliveredMessages, error) {
	pending, err := m.ListPendingForInstance(ctx, tx, instanceID)
	if err != nil {
		return DeliveredMessages{}, fmt.Errorf("DeliverPendingMessages: list pending: %w", err)
	}
	if len(pending) == 0 {
		return DeliveredMessages{}, nil
	}
	// @constraint: ListPendingForInstance already filters
	// `cancelled = FALSE` in SQL (both drivers), and orders by received_at
	// asc — pick the head row, one message per frame.
	oldest := &pending[0]
	ok, err := m.MarkDelivered(ctx, tx, oldest.ID, frameID, now)
	if err != nil {
		return DeliveredMessages{}, fmt.Errorf("DeliverPendingMessages: mark delivered %s: %w", oldest.ID, err)
	}
	if !ok {
		// @deliberate: concurrent delivery — another tx beat us; treat
		// as a no-op and let the next sweep retry whatever still pends.
		return DeliveredMessages{}, nil
	}
	row := *oldest
	row.DeliveredAt = &now
	f := frameID
	row.FrameID = &f
	return DeliveredMessages{Messages: []persistence.MessageRow{row}}, nil
}
