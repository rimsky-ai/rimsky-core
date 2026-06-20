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
		signalsByMessageID := make(map[shared.UUID]signalpkg.Signal, len(delivered.Messages))
		for _, msg := range delivered.Messages {
			msgSig := messageVirtualNodeSettleSignal(msg)
			signalsByMessageID[msg.ID] = msgSig
			if err := signalaudit.EmitSignal(ctx, persist.Events(),
				instanceID, shared.UUID{}, msgSig, now, tx); err != nil {
				return fmt.Errorf("emit message signal: %w", err)
			}
		}
		return cascadeMessageVirtualNodeSettleInTx(ctx, persist, queue, logger, tx,
			instanceID, frameID, delivered.Messages, signalsByMessageID, inst.TemplateHash,
			inst.MainRunScopeID)
	})
}

// @concept: message
// @concept: signal
func messageVirtualNodeSettleSignal(msg persistence.MessageRow) signalpkg.Signal {
	// @decision: empty-message-as-root-trigger
	// @story: empty-message-wakes-roots
	summaryTail := msg.Type
	if summaryTail == "" {
		summaryTail = "empty-wake"
	}
	return signalpkg.Signal{
		Type: signalpkg.TypePath("terminal/success"),
		Payload: map[string]any{
			"changed":          true,
			"attributes_delta": messagePayloadAsMap(msg.Payload),
			"change_summary":   "message-virtual-node:" + summaryTail,
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
func cascadeMessageVirtualNodeSettleInTx(
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
		return fmt.Errorf("cascadeMessageVirtualNodeSettleInTx: get template: %w", err)
	}
	if tmpl == nil {
		return nil
	}
	msgRefs := node.ExtractMessageRefsFromTemplate(tmpl.Spec)
	edges, err := node.BuildSubscriptionEdges(tmpl.Spec, msgRefs)
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
		msgSig, ok := signalsByMessageID[msg.ID]
		if !ok {
			msgSig = messageVirtualNodeSettleSignal(msg)
		}
		matched := edges.Match(msg.Type, successType)
		for _, e := range matched {
			if e.WhenExpr != nil {
				ok, _ := e.WhenExpr.Eval(msgSig)
				if !ok {
					continue
				}
			}
			receivers := byType[e.ReceiverNodeType]
			for _, r := range receivers {
				// @concept: cascade
				if !e.WakeOnChange {
					continue
				}
				var receiverScopeID shared.UUID
				if r.RunScopeID != nil {
					receiverScopeID = *r.RunScopeID
				} else {
					receiverScopeID = instanceMainRunScopeID
				}
				if err := persist.Nodes().AffirmNodeRunRow(ctx, r.ID, receiverScopeID, frameID, tx); err != nil {
					// @concept: run-scope
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
				if r.State == cascade.NodeStateParked {
					if err := wakeParkedReceiverWithDepsInTx(ctx, persist, queue, tx, r, frameID); err != nil {
						return fmt.Errorf("cascadeMessageVirtualNodeSettleInTx: wake parked %s: %w", r.ID, err)
					}
				} else {
					if err := persist.Nodes().MarkStaleForCascade(ctx, runID, frameID, tx); err != nil {
						return fmt.Errorf("cascadeMessageVirtualNodeSettleInTx: mark stale %s: %w", r.ID, err)
					}
				}
				// @story: upstream-pull-on-invalidate
				// @concept: cascade
				// @decision: synthetic-envelope-mechanism-retired
				if !e.SenderBoundToEmpty {
					if err := pullForceRefreshUpstreamsForMessageReceiver(
						ctx, persist, queue, logger, tx, r, runID, receiverScopeID, frameID,
						templateHash, byType,
					); err != nil {
						return fmt.Errorf("cascadeMessageVirtualNodeSettleInTx: pull upstream-refresh for %s: %w", r.ID, err)
					}
				}
			}
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
	args := RunArgs{Persist: persist, Queue: queue, Logger: logger}
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
