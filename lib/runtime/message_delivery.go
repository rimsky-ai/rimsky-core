// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: message
// @concept: frame

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

func EnqueueMessage(ctx context.Context, tx persistence.Tx, m persistence.MessagesTable, req persistence.EnqueueMessageRequest) error {
	if req.ID == (shared.UUID{}) {
		return errors.New("EnqueueMessage: id required")
	}
	if req.InstanceID == (shared.UUID{}) {
		return errors.New("EnqueueMessage: instance_id required")
	}
	// @decision: empty-message-as-root-trigger
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

type DeliveredMessages struct {
	Messages []persistence.MessageRow
}

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
			if err := deliverForRunningFrame(ctx, persist, queue, logger, f.InstanceID, f.FrameID, now); err != nil {
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

func deliverForRunningFrame(
	ctx context.Context, persist persistence.Tables, queue persistence.Queue,
	logger shared.Logger,
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
		emptyMessages := make([]persistence.MessageRow, 0)
		namedMessages := make([]persistence.MessageRow, 0)
		for _, msg := range delivered.Messages {
			if msg.Type == "" {
				emptyMessages = append(emptyMessages, msg)
				continue
			}
			namedMessages = append(namedMessages, msg)
		}
		if len(emptyMessages) > 0 {
			signalsByMessageID := make(map[shared.UUID]signalpkg.Signal, len(emptyMessages))
			for _, msg := range emptyMessages {
				msgSig := emptyMessageWakeSignal(msg)
				signalsByMessageID[msg.ID] = msgSig
				if err := signalaudit.EmitSignal(ctx, persist.Events(),
					instanceID, shared.UUID{}, msgSig, now, tx); err != nil {
					return fmt.Errorf("emit empty-message wake signal: %w", err)
				}
			}
			if err := cascadeEmptyMessageWakeInTx(ctx, persist, queue, logger, tx,
				instanceID, frameID, emptyMessages, signalsByMessageID, inst.TemplateHash,
				inst.MainRunScopeID); err != nil {
				return err
			}
		}
		for _, msg := range namedMessages {
			if err := deliverNamedMessageInTx(ctx, persist, logger, tx,
				instanceID, frameID, msg, inst.MainRunScopeID, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// @concept: message
// @concept: node-run
func deliverNamedMessageInTx(
	ctx context.Context, persist persistence.Tables,
	logger shared.Logger,
	tx persistence.Tx,
	instanceID, frameID shared.UUID,
	msg persistence.MessageRow,
	instanceMainRunScopeID shared.UUID,
	now time.Time,
) error {
	receiver, err := findMessageReceiverNode(ctx, persist, tx, instanceID, msg.Type)
	if err != nil {
		return fmt.Errorf("deliverNamedMessageInTx: find message-receiver-node for %q: %w", msg.Type, err)
	}
	if receiver == nil {
		if logger != nil {
			logger.Warn("deliverNamedMessageInTx: no message-receiver-node found; message delivered as dead letter",
				"instance_id", instanceID.String(),
				"message_type", msg.Type,
				"message_id", msg.ID.String())
		}
		return nil
	}
	runID, err := persist.Nodes().CreateNonCascadeStale(ctx, tx, persistence.NonCascadeStaleInput{
		NodeID:         receiver.ID,
		RunScopeID:     instanceMainRunScopeID,
		FrameID:        frameID,
		ExecutorName:   "",
		EnqueuedAt:     now,
		CreationReason: cascade.CreationReasonMessageDelivery,
	})
	if err != nil {
		return fmt.Errorf("deliverNamedMessageInTx: create message-receiver run for %q: %w", msg.Type, err)
	}
	body := messagePayloadAsMap(msg.Payload)
	if err := persist.NodeAttributes().Upsert(ctx, runID, receiver.ID, body, tx); err != nil {
		return fmt.Errorf("deliverNamedMessageInTx: upsert message body bag for %q: %w", msg.Type, err)
	}
	if err := persist.NodeAttributes().SetDispatchInputBag(ctx, tx, runID, receiver.ID, body); err != nil {
		return fmt.Errorf("deliverNamedMessageInTx: persist dispatch input bag for %q: %w", msg.Type, err)
	}
	return nil
}

// @concept: message
// @concept: node
func findMessageReceiverNode(
	ctx context.Context, persist persistence.Tables, tx persistence.Tx,
	instanceID shared.UUID, messageType string,
) (*persistence.NodeRow, error) {
	nodes, err := persist.Nodes().ListByInstance(ctx, instanceID, tx)
	if err != nil {
		return nil, err
	}
	for i := range nodes {
		if nodes[i].NodeType == messageType {
			return &nodes[i], nil
		}
	}
	return nil, nil
}

// @concept: message
// @concept: signal
// @decision: empty-message-as-root-trigger
// @story: empty-message-wakes-roots
func emptyMessageWakeSignal(msg persistence.MessageRow) signalpkg.Signal {
	return signalpkg.Signal{
		Type: signalpkg.TypePath("terminal/success"),
		Payload: map[string]any{
			"changed":          true,
			"attributes_delta": messagePayloadAsMap(msg.Payload),
			"change_summary":   "empty-message-wake",
		},
	}
}

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

// @concept: message
// @concept: cascade
// @concept: signal
func cascadeEmptyMessageWakeInTx(
	ctx context.Context, persist persistence.Tables, queue persistence.Queue,
	logger shared.Logger,
	tx persistence.Tx,
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
		return fmt.Errorf("cascadeEmptyMessageWakeInTx: get template: %w", err)
	}
	if tmpl == nil {
		return nil
	}
	msgRefs := node.ExtractMessageRefsFromTemplate(tmpl.Spec)
	edges, err := node.BuildSubscriptionEdges(tmpl.Spec, msgRefs)
	if err != nil {
		return fmt.Errorf("cascadeEmptyMessageWakeInTx: build edges: %w", err)
	}
	if edges == nil {
		return nil
	}
	instNodes, err := persist.Nodes().ListByInstance(ctx, instanceID, tx)
	if err != nil {
		return fmt.Errorf("cascadeEmptyMessageWakeInTx: list nodes: %w", err)
	}
	byType := make(map[string][]persistence.NodeRow, len(instNodes))
	for _, n := range instNodes {
		byType[n.NodeType] = append(byType[n.NodeType], n)
	}
	successType := signalpkg.TypePath("terminal/success")
	args := RunArgs{Persist: persist, Queue: queue, Logger: logger, Clock: shared.SystemClock{}}
	touchedReceiverRuns := map[shared.UUID]struct{}{}
	for _, msg := range messages {
		msgSig, ok := signalsByMessageID[msg.ID]
		if !ok {
			msgSig = emptyMessageWakeSignal(msg)
		}
		matched := edges.Match(msg.Type, successType)
		visitedReceivers := map[shared.UUID]struct{}{}
		for _, e := range matched {
			if e.WhenExpr != nil {
				ok, _ := e.WhenExpr.Eval(msgSig)
				if !ok {
					continue
				}
			}
			receivers := byType[e.ReceiverNodeType]
			for _, r := range receivers {
				// @concept: parked-state
				receiverScopeID := instanceMainRunScopeID
				if latest, err := persist.Nodes().GetLatestRunForNode(ctx, tx, r.ID); err != nil {
					return fmt.Errorf("cascadeEmptyMessageWakeInTx: latest run for receiver %s: %w", r.ID, err)
				} else if latest != nil {
					receiverScopeID = latest.RunScopeID
				}
				receiverRunID, hasReceiver, err := resolveReceiverRunForCascade(
					ctx, args, tx,
					r.ID, receiverScopeID, frameID, shared.UUID{}, shared.UUID{},
					visitedReceivers,
				)
				if err != nil {
					return fmt.Errorf("cascadeEmptyMessageWakeInTx: resolve receiver %s: %w", r.ID, err)
				}
				if !hasReceiver {
					continue
				}
				touchedReceiverRuns[receiverRunID] = struct{}{}
				// @story: upstream-pull-on-invalidate
				// @concept: cascade
				if !e.SenderBoundToEmpty {
					if err := pullForceRefreshUpstreamsForMessageReceiver(
						ctx, persist, queue, logger, tx, r, receiverRunID, receiverScopeID, frameID,
						templateHash, byType,
					); err != nil {
						return fmt.Errorf("cascadeEmptyMessageWakeInTx: pull upstream-refresh for %s: %w", r.ID, err)
					}
				}
			}
		}
	}
	for receiverRunID := range touchedReceiverRuns {
		if err := evaluateOneGate(ctx, args, tx, receiverRunID); err != nil {
			return fmt.Errorf("cascadeEmptyMessageWakeInTx: evaluate gate %s: %w", receiverRunID, err)
		}
	}
	return nil
}

// @story: upstream-pull-on-invalidate
// @concept: cascade
func pullForceRefreshUpstreamsForMessageReceiver(
	ctx context.Context, persist persistence.Tables, queue persistence.Queue,
	logger shared.Logger,
	tx persistence.Tx,
	receiver persistence.NodeRow,
	receiverRunID, targetRunScopeID, senderFrameID shared.UUID,
	templateHash string,
	byType map[string][]persistence.NodeRow,
) error {
	if logger == nil {
		logger = shared.SilentLogger{}
	}
	args := RunArgs{Persist: persist, Queue: queue, Logger: logger, Clock: shared.SystemClock{}}
	visited := map[shared.UUID]struct{}{receiver.ID: {}}
	return pullForceRefreshUpstreams(
		ctx, args, tx, receiver, byType,
		receiverRunID, targetRunScopeID, senderFrameID,
		templateHash, visited,
	)
}

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
	oldest := &pending[0]
	ok, err := m.MarkDelivered(ctx, tx, oldest.ID, frameID, now)
	if err != nil {
		return DeliveredMessages{}, fmt.Errorf("DeliverPendingMessages: mark delivered %s: %w", oldest.ID, err)
	}
	if !ok {
		return DeliveredMessages{}, nil
	}
	row := *oldest
	row.DeliveredAt = &now
	f := frameID
	row.FrameID = &f
	return DeliveredMessages{Messages: []persistence.MessageRow{row}}, nil
}
