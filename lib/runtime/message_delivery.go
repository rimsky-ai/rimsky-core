// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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
	"github.com/rimsky-ai/rimsky-core/lib/foundation/events"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/node"
)

type EnqueueMessageDeps interface {
	Instances() persistence.InstanceTable
	Templates() persistence.TemplateTable
	Messages() persistence.MessageTable
}

const (
	SenderKindOperator  = "operator"
	SenderKindPublisher = "publisher"
	SenderKindInstance  = "instance"
)

// @concept: message-schema
func EnqueueMessage(ctx context.Context, deps EnqueueMessageDeps, req persistence.EnqueueMessageRequest, tx persistence.Tx) error {
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
	// @decision: message-sender-kind-discriminator
	switch req.SenderKind {
	case SenderKindOperator, SenderKindPublisher, SenderKindInstance:
	default:
		return fmt.Errorf("EnqueueMessage: unknown sender_kind %q (want operator|publisher|instance)", req.SenderKind)
	}
	if err := validateMessagePayloadIsObjectShaped(req.Payload); err != nil {
		return &node.MessageBodySchemaViolation{Type: req.Type, Err: err}
	}
	if req.ReceivedAt.IsZero() {
		req.ReceivedAt = time.Now().UTC()
	}
	inst, err := deps.Instances().Get(ctx, req.InstanceID, tx)
	if err != nil {
		return fmt.Errorf("EnqueueMessage: get instance: %w", err)
	}
	if inst != nil {
		tpl, err := deps.Templates().GetByHash(ctx, inst.TemplateHash, tx)
		if err != nil {
			return fmt.Errorf("EnqueueMessage: get template: %w", err)
		}
		if tpl != nil {
			if err := node.ValidateMessageBody(&tpl.Spec, req.Type, req.Payload); err != nil {
				return fmt.Errorf("EnqueueMessage: %w", err)
			}
		}
	}
	if inst != nil && inst.MessageQueueMode == node.MessageQueueModeCoalesce {
		if _, err := deps.Messages().CancelPendingForInstance(ctx, req.InstanceID, tx); err != nil {
			return fmt.Errorf("EnqueueMessage: coalesce prior pending: %w", err)
		}
	}
	return deps.Messages().Insert(ctx, req, tx)
}

type DeliveredMessages struct {
	Messages []persistence.MessageRow
}

func SweepDeliverTriggeringMessagesForRunningFrames(
	ctx context.Context, persist persistence.Tables, logger shared.Logger, now time.Time,
) error {
	if persist == nil {
		return nil
	}
	pag := persistence.ListPagination{Limit: 256}
	for {
		var page persistence.PaginatedListResult[persistence.FrameRow]
		if err := persist.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
			unresolved := true
			p, err := persist.Frames().ListForObservability(ctx,
				persistence.FrameListFilter{Unresolved: &unresolved},
				pag, tx)
			page = p
			return err
		}); err != nil {
			return fmt.Errorf("SweepDeliverTriggeringMessagesForRunningFrames: list: %w", err)
		}
		for _, f := range page.Rows {
			if err := deliverForRunningFrame(ctx, persist, logger, f.InstanceID, f.FrameID, now); err != nil {
				if logger != nil {
					logger.Warn("SweepDeliverTriggeringMessagesForRunningFrames: deliver failed",
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
	ctx context.Context, persist persistence.Tables,
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
		frameRow, err := persist.Frames().GetForObservability(ctx, frameID, tx)
		if err != nil {
			return fmt.Errorf("get frame %s: %w", frameID, err)
		}
		if frameRow == nil {
			return nil
		}
		delivered, err := DeliverTriggeringMessage(ctx, persist.Messages(), instanceID, frameID, frameRow.TriggeringMessageID, now, tx)
		if err != nil {
			return err
		}
		if len(delivered.Messages) == 0 {
			return nil
		}
		for _, msg := range delivered.Messages {
			if err := deliverNamedMessageInTx(ctx, persist, logger, instanceID, frameID, msg, frameRow.RootRunScopeID, now, tx); err != nil {
				return err
			}
		}
		return nil
	})
}

// @concept: message
// @concept: node-run
func deliverNamedMessageInTx(
	ctx context.Context, persist persistence.Tables, logger shared.Logger, instanceID, frameID shared.UUID, msg persistence.MessageRow, frameRootRunScopeID shared.UUID, now time.Time, tx persistence.Tx,
) error {
	receiver, err := findMessageReceiverNode(ctx, persist, instanceID, msg.Type, tx)
	if err != nil {
		return fmt.Errorf("deliverNamedMessageInTx: find message-receiver-node for %q: %w", msg.Type, err)
	}
	if receiver == nil {
		if logger != nil {
			logger.Warn("deliverNamedMessageInTx: no message-receiver-node found; recording dead-letter audit event",
				"instance_id", instanceID.String(),
				"message_type", msg.Type,
				"message_id", msg.ID.String())
		}
		instID := instanceID
		if err := persist.Events().Append(ctx, persistence.EventAppendInput{
			InstanceID: &instID,
			Kind:       events.KindMessageDeadLettered(),
			Payload: map[string]any{
				"message_id":   msg.ID.String(),
				"message_type": msg.Type,
				"reason":       "no_message_receiver_node",
			},
		}, tx); err != nil {
			return fmt.Errorf("deliverNamedMessageInTx: append dead-letter audit event for %q: %w", msg.Type, err)
		}
		return nil
	}
	runID, err := persist.Nodes().CreateNonCascadeStale(ctx, persistence.NonCascadeStaleInput{
		NodeID:         receiver.ID,
		RunScopeID:     frameRootRunScopeID,
		FrameID:        frameID,
		ExecutorName:   "",
		EnqueuedAt:     now,
		CreationReason: cascade.CreationReasonMessageDelivery,
	}, tx)
	if err != nil {
		return fmt.Errorf("deliverNamedMessageInTx: create message-receiver run for %q: %w", msg.Type, err)
	}
	body := messagePayloadAsMap(msg.Payload)
	if err := persist.NodeAttributes().Upsert(ctx, runID, receiver.ID, body, tx); err != nil {
		return fmt.Errorf("deliverNamedMessageInTx: upsert message body bag for %q: %w", msg.Type, err)
	}
	if err := persist.NodeAttributes().SetDispatchInputBag(ctx, runID, receiver.ID, body, tx); err != nil {
		return fmt.Errorf("deliverNamedMessageInTx: persist dispatch input bag for %q: %w", msg.Type, err)
	}
	return nil
}

// @concept: message
// @concept: node
func findMessageReceiverNode(
	ctx context.Context, persist persistence.Tables, instanceID shared.UUID, messageType string, tx persistence.Tx,
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
func validateMessagePayloadIsObjectShaped(payload []byte) error {
	if len(payload) == 0 {
		return nil
	}
	var probe any
	if err := json.Unmarshal(payload, &probe); err != nil {
		return fmt.Errorf("payload is not valid JSON: %w", err)
	}
	if probe == nil {
		return nil
	}
	if _, ok := probe.(map[string]any); !ok {
		return fmt.Errorf("payload must be a JSON object (or empty) to populate the receiving run's attribute bag; got %T", probe)
	}
	return nil
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

func DeliverTriggeringMessage(
	ctx context.Context, m persistence.MessageTable, instanceID shared.UUID, frameID shared.UUID, triggeringMessageID shared.UUID, now time.Time, tx persistence.Tx,
) (DeliveredMessages, error) {
	msg, err := m.Get(ctx, triggeringMessageID, tx)
	if err != nil {
		return DeliveredMessages{}, fmt.Errorf("DeliverTriggeringMessage: get trigger %s: %w", triggeringMessageID, err)
	}
	if msg == nil {
		return DeliveredMessages{}, nil
	}
	if msg.InstanceID != instanceID {
		return DeliveredMessages{}, nil
	}
	if msg.DeliveredAt != nil || msg.Cancelled {
		return DeliveredMessages{}, nil
	}
	ok, err := m.MarkDelivered(ctx, triggeringMessageID, frameID, now, tx)
	if err != nil {
		return DeliveredMessages{}, fmt.Errorf("DeliverTriggeringMessage: mark delivered %s: %w", triggeringMessageID, err)
	}
	if !ok {
		return DeliveredMessages{}, nil
	}
	row := *msg
	row.DeliveredAt = &now
	f := frameID
	row.FrameID = &f
	return DeliveredMessages{Messages: []persistence.MessageRow{row}}, nil
}
