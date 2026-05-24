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
//   - `coalesce` (default): deliver all pending messages into the new
//     frame.
//   - `serial_queue`: deliver the oldest one message; leave the rest
//     pending.
//
// @blessed-invariant: messages are inert in rimsky. The delivery path
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
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fallguy/rimsky/foundation/persistence"
	"github.com/fallguy/rimsky/foundation/shared"
	"github.com/fallguy/rimsky/foundation/spec"
	"github.com/fallguy/rimsky/graph/node"
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
	// FrameDeliveryCoalesce delivers all pending messages into the new
	// frame. The default.
	FrameDeliveryCoalesce FrameDeliveryMode = "coalesce"
	// FrameDeliverySerialQueue delivers the oldest pending message;
	// remaining messages stay pending until the next frame.
	FrameDeliverySerialQueue FrameDeliveryMode = "serial_queue"
)

// DeliveredMessages is the return shape of DeliverPendingMessages —
// the messages that were stamped delivered_at + frame_id in this call.
// Callers (the frame-boundary path) use this to drive cascade /
// stale-mark walks for subscriber resolution.
type DeliveredMessages struct {
	Messages []persistence.MessageRow
}

// DeliverPendingMessages is invoked at frame-boundary creation.
// Selects pending messages for the instance, picks the subset per
// `mode`, marks them delivered with the new frame_id, and returns the
// rows so the caller can drive subscription matching.
//
// Cancelled messages: pre-cancelled rows
// (col:rimsky_messages.cancelled = TRUE) are skipped — they were
// stamped delivered_at=now() + frame_id=NULL by `CancelBackfill` and
// surface in the dead-letter slice for diagnostics. Spec
// §Backfills / cancelled column.
//
// Dead-lettering of zero-subscriber matches is handled by the caller
// (the subscription-walk return value tells the caller whether any
// subscribers fired).
// SweepDeliverMessagesForRunningFrames is the frame-boundary sweep that
// the scheduler invokes after `graph/frame.RunTick` promotes queued
// frames to running. For each running frame whose owning instance still
// has pending messages, the sweep loads the per-instance
// `frame_delivery_mode` (col:rimsky_instances.frame_delivery_mode) and
// calls `DeliverPendingMessages` inside its own short tx.
//
// Idempotent: a message that's already been marked delivered_at + frame_id
// is filtered out by `MessagesTable.ListPendingForInstance`, so re-running
// the sweep per tick is safe. Per-frame work is bounded by the number of
// pending messages — if no messages are pending for a given instance the
// helper returns immediately.
//
// Implementation note: the sweep paginates `FramesTable.ListForObservability`
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
// back to coalesce so a defaulted column does the safe thing.
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
		mode := FrameDeliveryCoalesce
		if inst.FrameDeliveryMode == string(FrameDeliverySerialQueue) {
			mode = FrameDeliverySerialQueue
		}
		delivered, err := DeliverPendingMessages(ctx, tx, persist.Messages(),
			instanceID, frameID, mode, now)
		if err != nil {
			return err
		}
		if len(delivered.Messages) == 0 {
			return nil
		}
		return cascadeMessageSubscribersInTx(ctx, persist, queue, tx,
			instanceID, frameID, delivered.Messages, inst.TemplateHash)
	})
}

// cascadeMessageSubscribersInTx walks subscription edges keyed on
// TopicKind="message" and stale-marks every receiver node in the
// instance whose envelope filters match each just-delivered message.
//
// Runs inside the caller's tx so the stale-mark commits atomically
// with the MarkDelivered write — without this gating in the same tx, a
// re-tick that arrived between MarkDelivered and stale-mark would treat
// the message as already delivered and never wake the receivers.
//
// Match semantics per spec §Subscriptions on messages: empty filter
// fields match any value; non-empty fields must equal the envelope
// field. The reserved value `target: "self"` resolves to the
// subscribing node's own alias (the template-relative `Name` of the
// receiver). Cross-cutting (`instance: true`) message subscriptions
// are honored by walking the empty-key bucket; per-node subscriptions
// without a `node:` (the normal shape for `on: message`) also land
// under the empty key.
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
	subRefs := node.ExtractSubstitutionRefsFromTemplate(tmpl.Spec)
	edges := node.BuildSubscriptionEdges(tmpl.Spec, subRefs)
	// Collect every "message" topic edge across all sender keys. Message
	// subscriptions usually land under the empty-key bucket (no upstream
	// sender node-type), but we tolerate any senderKey to keep the walk
	// honest about future template shapes.
	var messageEdges []node.SubscriptionEdge
	for _, list := range edges {
		for _, e := range list {
			if e.TopicKind == spec.TopicKindMessage {
				messageEdges = append(messageEdges, e)
			}
		}
	}
	if len(messageEdges) == 0 {
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
		for _, e := range messageEdges {
			if !messageEdgeMatches(e, msg) {
				continue
			}
			receivers := byType[e.ReceiverNodeType]
			for _, r := range receivers {
				// `target: self` filter applies to messages whose
				// envelope target equals the receiver's own
				// template-relative node-type (the alias). Skip
				// receivers whose alias doesn't match the envelope
				// target when the subscription declared `target: self`.
				// Empty `msg.Target` is explicitly NOT a self-target —
				// senders use `*` for broadcast; an unaddressed envelope
				// has no target and never matches a `target: self`
				// subscription. Spec §Unified message layer /
				// Subscriptions.
				if e.Filter.Target == "self" && msg.Target != r.NodeType {
					continue
				}
				// Resolve receiver's RunScope. If the LATERAL didn't
				// project one (no in-flight row), default to the
				// instance's main RunScope — message cascade is intra-
				// scope on the main RunScope. AffirmNodeRunRow inserts
				// a pending row keyed on (receiver_node_id, scope) so
				// MarkStaleForCascade has a row to UPDATE.
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
					// Defensive: a closed RunScope means the
					// receiver's scope has terminated (parent
					// rendezvous has fired). The walker MUST NOT
					// cross into closed RunScopes — skip this
					// receiver and continue the cascade walk per
					// concept:run-scope. Without this, a benign race
					// with scope closure surfaces as a cascade-walk
					// abort that strands the rest of the receivers.
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

// messageEdgeMatches reports whether a subscription edge's
// envelope-filter dimensions accept the given message envelope. Empty
// filter fields are wildcards. The `target: self` value is handled at
// the receiver-resolution step (cascadeMessageSubscribersInTx) since
// "self" is receiver-relative.
func messageEdgeMatches(e node.SubscriptionEdge, msg persistence.MessageRow) bool {
	if e.Filter.Kind != "" && e.Filter.Kind != msg.Kind {
		return false
	}
	if e.Filter.Sender != "" && e.Filter.Sender != msg.Sender {
		return false
	}
	if e.Filter.SenderKind != "" && e.Filter.SenderKind != msg.SenderKind {
		return false
	}
	// Target filter: explicit non-"self" target must equal envelope.
	// "self" is filtered at the receiver-resolution step.
	if e.Filter.Target != "" && e.Filter.Target != "self" && e.Filter.Target != msg.Target {
		return false
	}
	return true
}

func DeliverPendingMessages(
	ctx context.Context, tx persistence.Tx, m persistence.MessagesTable,
	instanceID shared.UUID, frameID shared.UUID, mode FrameDeliveryMode,
	now time.Time,
) (DeliveredMessages, error) {
	pending, err := m.ListPendingForInstance(ctx, tx, instanceID)
	if err != nil {
		return DeliveredMessages{}, fmt.Errorf("DeliverPendingMessages: list pending: %w", err)
	}
	// Filter out cancelled rows; ordering is already by received_at asc.
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
	default: // coalesce
		deliverSet = live
	}
	delivered := make([]persistence.MessageRow, 0, len(deliverSet))
	for _, msg := range deliverSet {
		ok, err := m.MarkDelivered(ctx, tx, msg.ID, frameID, now)
		if err != nil {
			return DeliveredMessages{}, fmt.Errorf("DeliverPendingMessages: mark delivered %s: %w", msg.ID, err)
		}
		if !ok {
			// Concurrent delivery — skip.
			continue
		}
		msg.DeliveredAt = &now
		f := frameID
		msg.FrameID = &f
		delivered = append(delivered, msg)
	}
	return DeliveredMessages{Messages: delivered}, nil
}
