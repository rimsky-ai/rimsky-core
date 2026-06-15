// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// message_delivery.go — E5. Message queue + delivery at frame boundary.
//
// Spec
// .ok-planner/specs/2026-05-17-sensor-messaging-unification-design.md
// §Publisher protocol unification. Messages are envelopes that drive
// instance frame creation; operators and publishers (bundled sensors)
// both emit messages via the generic
// `POST /instances/{id}/messages` endpoint. At frame creation the
// scheduler delivers pending messages, walks subscriptions, and
// stale-marks matching receivers.
//
// @concept: message
// @concept: frame
//
// Delivery semantics follow the instance's `frame_delivery_mode`
// (col:rimsky_instances.frame_delivery_mode):
//   - `serial_queue` (default): deliver the oldest one message; leave
//     the rest pending. One message per frame, so each backfill is its
//     own rerun/override — unambiguous.
//   - `coalesce`: deliver pending messages in received-order, coalescing
//     until a message would resolve a payload-reading node's
//     substitution to a value different from one already accumulated in
//     this frame, then stop (the rest stay pending for the next frame).
//     Same-value (idempotent) bindings keep coalescing.
//
// @blessed-invariant: message-inertness — messages are inert in rimsky. The delivery path
// touches envelope routing fields (kind, sender, sender_kind, target,
// frame_id, delivered_at) but never the `payload` bytes. The two
// sanctioned read sites for payload bytes live elsewhere: the
// substitution leaf in `graph/attribute/substitution.go::resolveTrigger`
// (which walks payload via `walkPath`) and the persistence-layer fetch
// in `control/controlapi/messages.go::handleGetMessage` (which surfaces
// the row verbatim to the operator). Same opacity discipline as
// `@blessed-invariant 20/21`.

package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	signalaudit "github.com/rimsky-ai/rimsky-core/lib/foundation/signal/audit"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

// EnqueueMessage inserts a message envelope into rimsky_messages with
// delivered_at NULL. Validates Kind / SenderKind. Caller passes a
// pre-allocated ID so the message id can flow back to the operator on
// `POST /instances/{id}/messages`.
func EnqueueMessage(ctx context.Context, tx persistence.Tx, m persistence.MessagesTable, req persistence.EnqueueMessageRequest) error {
	if req.ID == (shared.UUID{}) {
		return errors.New("EnqueueMessage: id required")
	}
	if req.InstanceID == (shared.UUID{}) {
		return errors.New("EnqueueMessage: instance_id required")
	}
	if req.Kind == "" {
		return errors.New("EnqueueMessage: kind required")
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

// FrameDeliveryMode discriminates the per-instance delivery semantics.
// Persisted on col:rimsky_instances.frame_delivery_mode.
type FrameDeliveryMode string

const (
	// FrameDeliveryCoalesce delivers pending messages in received-order,
	// coalescing until a message would bind a payload-reading node's
	// substitution to a value that conflicts with one already accumulated
	// in this frame; the rest stay pending for the next frame. The opt-in
	// "fancy" mode.
	FrameDeliveryCoalesce FrameDeliveryMode = "coalesce"
	// FrameDeliverySerialQueue delivers the oldest pending message;
	// remaining messages stay pending until the next frame. The default —
	// one message per frame, so each backfill is its own rerun/override.
	FrameDeliverySerialQueue FrameDeliveryMode = "serial_queue"
)

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
// has pending messages, the sweep loads the per-instance
// `frame_delivery_mode` (col:rimsky_instances.frame_delivery_mode) and
// drives the per-instance delivery inside its own short tx — selecting
// pending messages, picking the subset per `mode`, marking them
// delivered with the new frame_id, and returning the rows so the
// caller can drive subscription matching. Cancelled rows
// (col:rimsky_messages.cancelled = TRUE) are skipped — they were
// stamped delivered_at=now() + frame_id=NULL by `CancelBackfill` and
// surface in the dead-letter slice for diagnostics (spec
// §Backfills / cancelled column). Dead-lettering of zero-subscriber
// matches is handled by the caller (the subscription-walk return value
// tells the caller whether any subscribers fired).
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

// deliverForRunningFrame loads the per-instance delivery mode and calls
// DeliverPendingMessages in a short transaction. Empty/unknown mode falls
// back to serial_queue (the default — one message per frame) so a
// defaulted column does the safe, unambiguous thing.
//
// After marking messages delivered, walks the per-template subscription
// edges keyed on `TopicKind="message"` and stale-marks every receiver
// node whose envelope filters (`kind`, `sender`, `sender_kind`,
// `target`) match the just-delivered envelope. Per spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Delivery: "Matched subscribers' nodes are stale-marked within the
// new frame."
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
		mode := FrameDeliverySerialQueue
		if inst.FrameDeliveryMode == string(FrameDeliveryCoalesce) {
			mode = FrameDeliveryCoalesce
		}
		// @deliberate: only build the conflict resolver under coalesce —
		// serial_queue delivers one message regardless, so the per-template
		// subscription-edge load is wasted work in that mode.
		var resolver coalesceConflictResolver
		if mode == FrameDeliveryCoalesce {
			resolver, err = buildCoalesceConflictResolver(ctx, persist, tx, inst.TemplateHash)
			if err != nil {
				return err
			}
		}
		delivered, err := DeliverPendingMessages(ctx, tx, persist.Messages(),
			instanceID, frameID, mode, now, resolver)
		if err != nil {
			return err
		}
		if len(delivered.Messages) == 0 {
			return nil
		}
		// @concept: signal — emit one message/<kind>/<sender_kind>/<target>
		// signal per delivered envelope. The receiver-side cascade walk in
		// cascadeMessageSubscribersInTx below drives stale-marks
		// independently; signal-emit and cascade-walk converge under the
		// Pass 2 signal-driven reshape.
		for _, msg := range delivered.Messages {
			target := msg.Target
			if target == "" {
				target = "_unspecified"
			}
			msgSig := signalpkg.Signal{
				Type: signalpkg.TypePath(fmt.Sprintf("message/%s/%s/%s",
					msg.Kind, msg.SenderKind, target)),
				Payload: map[string]any{
					"kind":            msg.Kind,
					"sender_kind":     msg.SenderKind,
					"sender":          msg.Sender,
					"target":          msg.Target,
					"message_payload": messagePayloadAsMap(msg.Payload),
				},
			}
			if err := signalaudit.EmitSignal(ctx, persist.Events(),
				instanceID, shared.UUID{}, msgSig, now, tx); err != nil {
				return fmt.Errorf("emit message signal: %w", err)
			}
		}
		return cascadeMessageSubscribersInTx(ctx, persist, queue, tx,
			instanceID, frameID, delivered.Messages, inst.TemplateHash)
	})
}

// messagePayloadAsMap decodes the message envelope's payload bytes
// into a map[string]any when JSON-shaped; falls back to a stub map
// carrying byte length. Per @blessed-invariant 21 (messages are
// inert) we don't transform the bytes for any other purpose — this
// shape exists only so subscriber CEL when: predicates can read
// into payload.message_payload.foo at evaluation time.
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

// cascadeMessageSubscribersInTx fires the message-cascade for every
// just-delivered message by routing the corresponding
// `message/<kind>/<sender_kind>/<target>` signal through the
// subscriber-driven cascade walker. Under the 2026-05-23 signal-
// taxonomy reshape, message subscribers are ordinary subscription
// edges with `type: message/...` (exact or prefix); the per-envelope
// filter dimensions (kind / sender / sender_kind / target) move into
// CEL `when:` predicates on `payload`.
//
// Runs inside the caller's tx so the stale-mark commits atomically
// with the MarkDelivered write — without this gating in the same tx, a
// re-tick that arrived between MarkDelivered and stale-mark would treat
// the message as already delivered and never wake the receivers.
//
// The reserved `target: "self"` semantic (subscriber wants only
// envelopes addressed to its own alias) is expressed by the CEL
// predicate `when: payload.target == self_alias` at registration —
// the validator substitutes the receiver's own type for `self_alias`.
// For backward compatibility with existing templates that still spell
// `target: self`, the runtime falls back to per-receiver alias
// matching when an edge's pattern is exactly `message/*/*/self`.
func cascadeMessageSubscribersInTx(
	ctx context.Context, persist persistence.Tables, queue persistence.Queue, tx persistence.Tx,
	instanceID, frameID shared.UUID, messages []persistence.MessageRow,
	templateHash string,
) error {
	if len(messages) == 0 {
		return nil
	}
	tmpl, err := persist.Templates().GetByHash(ctx, templateHash, tx)
	if err != nil {
		return fmt.Errorf("cascadeMessageSubscribersInTx: get template: %w", err)
	}
	if tmpl == nil {
		return nil
	}
	edges, err := node.BuildSubscriptionEdges(tmpl.Spec)
	if err != nil {
		return fmt.Errorf("cascadeMessageSubscribersInTx: build edges: %w", err)
	}
	if edges == nil {
		return nil
	}
	instNodes, err := persist.Nodes().ListByInstance(ctx, instanceID, tx)
	if err != nil {
		return fmt.Errorf("cascadeMessageSubscribersInTx: list nodes: %w", err)
	}
	byType := make(map[string][]persistence.NodeRow, len(instNodes))
	for _, n := range instNodes {
		byType[n.NodeType] = append(byType[n.NodeType], n)
	}
	for _, msg := range messages {
		msgSigType := signalpkg.TypePath(fmt.Sprintf("message/%s/%s/%s",
			msg.Kind, msg.SenderKind, msg.Target))
		// @constraint: broadcast envelopes carry `target == ""` on the
		// wire; rewrite to "_unspecified" so the signal type-path doesn't
		// collapse into a trailing-slash form.
		if msg.Target == "" {
			msgSigType = signalpkg.TypePath(fmt.Sprintf("message/%s/%s/_unspecified",
				msg.Kind, msg.SenderKind))
		}
		msgPayload := map[string]any{
			"kind":            msg.Kind,
			"sender_kind":     msg.SenderKind,
			"sender":          msg.Sender,
			"target":          msg.Target,
			"message_payload": messagePayloadAsMap(msg.Payload),
		}
		msgSig := signalpkg.Signal{Type: msgSigType, Payload: msgPayload}
		// @constraint: messages cross-cut and have no sender-node-type;
		// match against the empty sender-key bucket.
		matched := edges.Match("", msgSigType)
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
				// @constraint: legacy `target: self` compatibility —
				// a subscription pattern ending in `/self` matches only
				// envelopes whose target equals the receiver's own alias.
				if strings.HasSuffix(string(e.TypePattern), "/self") && msg.Target != r.NodeType {
					continue
				}
				// @concept: cascade
				// @constraint: wake-up effects (affirm + mark-stale +
				// enqueue-frame) gate on wake_on_change. A subscription
				// with wake_on_change: false skips the wake-up path here —
				// the message is still routed via the signal-emit above
				// for audit, but the receiver is not stale-marked or
				// enqueued.
				//
				// @deliberate: message cascade has no wait-set surface (the
				// receiver reads from the delivered message envelope itself,
				// not from a sender node's attribute), so there is no wait-
				// set insert to preserve outside this gate. The equivalent
				// "still needs to read the sender's value" property for
				// messages is automatic: the message row stays
				// delivered_at/frame_id stamped and is readable via the
				// receiver's substitution context whenever the receiver
				// eventually dispatches via another edge.
				if !e.WakeOnChange {
					continue
				}
				// @deliberate: default to the instance's main RunScope
				// when the LATERAL didn't project one — message cascade is
				// intra-scope on the main RunScope, and AffirmNodeRunRow
				// must insert a pending row keyed on (receiver_node_id,
				// scope) so MarkStaleForCascade has a row to UPDATE.
				var receiverScopeID shared.UUID
				if r.RunScopeID != nil {
					receiverScopeID = *r.RunScopeID
				} else {
					inst, err := persist.Instances().Get(ctx, instanceID, tx)
					if err != nil {
						return fmt.Errorf("cascadeMessageSubscribersInTx: get instance %s: %w", instanceID, err)
					}
					if inst == nil {
						continue
					}
					receiverScopeID = inst.MainRunScopeID
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
					return fmt.Errorf("cascadeMessageSubscribersInTx: affirm receiver run %s: %w", r.ID, err)
				}
				runID, ok, err := queue.GetInFlightRunForNode(ctx, tx, r.ID, receiverScopeID)
				if err != nil {
					return fmt.Errorf("cascadeMessageSubscribersInTx: resolve receiver run %s: %w", r.ID, err)
				}
				if !ok {
					continue
				}
				if err := persist.Nodes().MarkStaleForCascade(ctx, runID, frameID, tx); err != nil {
					return fmt.Errorf("cascadeMessageSubscribersInTx: mark stale %s: %w", r.ID, err)
				}
			}
		}
	}
	return nil
}

// coalesceConflictResolver returns, for one pending message, the set of
// payload-reading receiver node types the message would invalidate (the
// same set the delivery cascade later stale-marks). nil means "no
// resolver" — conflict detection is skipped and coalesce delivers every
// pending message (the legacy behavior, used by the pure unit fakes that
// have no template). Returning an empty set means the message matches no
// payload-reading node, so it can never conflict.
type coalesceConflictResolver func(msg persistence.MessageRow) (matchedReceiverTypes []string)

// buildCoalesceConflictResolver loads the per-template subscription edges
// once and returns a resolver that maps a message to the receiver node
// types it would invalidate. It reuses the exact match path
// (`BuildSubscriptionEdges` → `edges.Match` + CEL `when:` eval) that
// `cascadeMessageSubscribersInTx` runs after delivery, so the conflict
// decision and the stale-mark agree on which nodes a message touches.
func buildCoalesceConflictResolver(
	ctx context.Context, persist persistence.Tables, tx persistence.Tx, templateHash string,
) (coalesceConflictResolver, error) {
	tmpl, err := persist.Templates().GetByHash(ctx, templateHash, tx)
	if err != nil {
		return nil, fmt.Errorf("buildCoalesceConflictResolver: get template: %w", err)
	}
	if tmpl == nil {
		return nil, nil
	}
	edges, err := node.BuildSubscriptionEdges(tmpl.Spec)
	if err != nil {
		return nil, fmt.Errorf("buildCoalesceConflictResolver: build edges: %w", err)
	}
	if edges == nil {
		return func(persistence.MessageRow) []string { return nil }, nil
	}
	return func(msg persistence.MessageRow) []string {
		target := msg.Target
		if target == "" {
			target = "_unspecified"
		}
		msgSigType := signalpkg.TypePath(fmt.Sprintf("message/%s/%s/%s",
			msg.Kind, msg.SenderKind, target))
		msgSig := signalpkg.Signal{
			Type: msgSigType,
			Payload: map[string]any{
				"kind":            msg.Kind,
				"sender_kind":     msg.SenderKind,
				"sender":          msg.Sender,
				"target":          msg.Target,
				"message_payload": messagePayloadAsMap(msg.Payload),
			},
		}
		// @constraint: messages cross-cut — match against the empty
		// sender-key bucket (same call cascadeMessageSubscribersInTx
		// makes, so conflict detection and stale-mark agree).
		matched := edges.Match("", msgSigType)
		var receivers []string
		for _, e := range matched {
			if e.WhenExpr != nil {
				ok, _ := e.WhenExpr.Eval(msgSig)
				if !ok {
					continue
				}
			}
			receivers = append(receivers, e.ReceiverNodeType)
		}
		return receivers
	}, nil
}

// DeliverPendingMessages selects pending messages for the instance, picks
// the deliver-set per `mode`, marks them delivered with the new frame_id,
// and returns the rows so the caller can drive subscription matching.
//
// Delivery-set selection by mode:
//   - serial_queue: the oldest one message; the rest stay pending.
//   - coalesce: messages in strict received-order, coalescing until a
//     message would bind a payload-reading node's substitution to a value
//     conflicting with one already accumulated in this frame — then stop
//     (the rest stay pending for the next frame). Same-value (idempotent)
//     bindings keep coalescing. This is the load-bearing no-silent-loss
//     property: two messages that would resolve the *same* node to
//     *different* values MUST land in *separate* frames, so a distinct
//     backfill override is never silently collapsed into another's rerun.
//
// `resolve` (coalesce only) maps a candidate message to the payload-
// reading receiver node types it would invalidate; a nil resolver skips
// conflict detection and coalesces everything (legacy behavior).
func DeliverPendingMessages(
	ctx context.Context, tx persistence.Tx, m persistence.MessagesTable,
	instanceID shared.UUID, frameID shared.UUID, mode FrameDeliveryMode,
	now time.Time, resolve coalesceConflictResolver,
) (DeliveredMessages, error) {
	pending, err := m.ListPendingForInstance(ctx, tx, instanceID)
	if err != nil {
		return DeliveredMessages{}, fmt.Errorf("DeliverPendingMessages: list pending: %w", err)
	}
	live := make([]persistence.MessageRow, 0, len(pending))
	for _, r := range pending {
		if r.Cancelled {
			continue
		}
		live = append(live, r)
	}
	if len(live) == 0 {
		return DeliveredMessages{}, nil
	}
	var deliverSet []persistence.MessageRow
	switch mode {
	case FrameDeliverySerialQueue:
		deliverSet = live[:1]
	default:
		deliverSet = coalesceDeliverSet(live, resolve)
	}
	delivered := make([]persistence.MessageRow, 0, len(deliverSet))
	for _, msg := range deliverSet {
		ok, err := m.MarkDelivered(ctx, tx, msg.ID, frameID, now)
		if err != nil {
			return DeliveredMessages{}, fmt.Errorf("DeliverPendingMessages: mark delivered %s: %w", msg.ID, err)
		}
		if !ok {
			continue
		}
		msg.DeliveredAt = &now
		f := frameID
		msg.FrameID = &f
		delivered = append(delivered, msg)
	}
	return DeliveredMessages{Messages: delivered}, nil
}

// coalesceDeliverSet walks `live` (received-order, ascending) and
// accumulates messages into the frame until one would bind a payload-
// reading node to a value conflicting with one an already-accumulated
// message bound for the same node. The conflicting message — and every
// message after it — stays pending for the next frame.
//
// @blessed-invariant: no-silent-override-coalesce — no silent override loss under coalesce. Two
// messages that would resolve the SAME payload-reading node to DIFFERENT
// values land in SEPARATE frames; only same-value (idempotent) bindings
// coalesce into one. A distinct backfill `partition_request_override` is
// therefore never silently collapsed into another override's rerun. The
// comparison is conservative: when two messages match a common receiver
// node type, any payload difference is treated as a conflict (we don't
// inspect which substitution slot the node reads), and received-order is
// always preserved.
//
// A nil resolver (no template — the pure unit fakes) skips detection and
// delivers everything, the legacy coalesce behavior.
func coalesceDeliverSet(live []persistence.MessageRow, resolve coalesceConflictResolver) []persistence.MessageRow {
	if resolve == nil || len(live) <= 1 {
		return live
	}
	// @constraint: boundPayload tracks per-receiver-type the payload of
	// the accumulated message that bound it; a later message matching the
	// same receiver type with a different payload is a conflict.
	boundPayload := make(map[string][]byte)
	accepted := 0
	for _, msg := range live {
		receivers := resolve(msg)
		conflict := false
		for _, rt := range receivers {
			prev, seen := boundPayload[rt]
			if seen && !bytes.Equal(prev, msg.Payload) {
				conflict = true
				break
			}
		}
		// @constraint: on a payload conflict the loop stops; this
		// message and the rest must wait for the next frame so the
		// receiver observes a single bound payload per frame.
		if conflict {
			break
		}
		for _, rt := range receivers {
			if _, seen := boundPayload[rt]; !seen {
				boundPayload[rt] = msg.Payload
			}
		}
		accepted++
	}
	if accepted == 0 {
		// @deliberate: unreachable in practice — the first message
		// enters with an empty boundPayload map so accepted is always
		// ≥ 1; this guard ensures we never return an empty set when
		// there is live work.
		return live[:1]
	}
	return live[:accepted]
}
