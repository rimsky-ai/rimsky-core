// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: message-emitter-node
// @concept: message

package runtime

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/graph/frame"
)

// @story: cascade-emit
// @concept: message-emitter-node
func emitCascadeMessage(
	ctx context.Context,
	tables persistence.Tables,
	instanceID, nodeID, frameID shared.UUID,
	emitMessageType string,
	body []byte,
) (shared.UUID, bool, error) {
	var messageID shared.UUID
	var replayed bool
	err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var ierr error
		messageID, replayed, ierr = emitCascadeMessageInTx(ctx, tables, tx,
			instanceID, nodeID, frameID, emitMessageType, body)
		return ierr
	})
	return messageID, replayed, err
}

// @story: cascade-emit
// @concept: message-emitter-node
func emitCascadeMessageInTx(
	ctx context.Context,
	tables persistence.Tables,
	tx persistence.Tx,
	instanceID, nodeID, frameID shared.UUID,
	emitMessageType string,
	body []byte,
) (shared.UUID, bool, error) {
	if instanceID == (shared.UUID{}) {
		return shared.UUID{}, false, fmt.Errorf("emitCascadeMessageInTx: instance_id required")
	}
	if nodeID == (shared.UUID{}) {
		return shared.UUID{}, false, fmt.Errorf("emitCascadeMessageInTx: node_id required")
	}
	if frameID == (shared.UUID{}) {
		return shared.UUID{}, false, fmt.Errorf("emitCascadeMessageInTx: frame_id required")
	}
	if emitMessageType == "" {
		return shared.UUID{}, false, fmt.Errorf("emitCascadeMessageInTx: emits_message type required")
	}
	if body == nil {
		body = []byte(`{}`)
	}

	idempotencyKey := fmt.Sprintf("cascade-emit:%s:%s", nodeID.String(), frameID.String())
	senderKind := "instance"
	sender := "instance:" + instanceID.String()

	candidateID := shared.UUID(uuid.New())

	dedupRow, inserted, err := tables.MessageIdempotencies().InsertOrLookup(ctx, tx, persistence.MessageIdempotencyRow{
		InstanceID:     instanceID,
		SenderKind:     senderKind,
		Sender:         sender,
		SenderSubject:  "",
		IdempotencyKey: idempotencyKey,
		MessageID:      candidateID,
	})
	if err != nil {
		return shared.UUID{}, false, fmt.Errorf("emitCascadeMessageInTx: idempotency upsert: %w", err)
	}
	if !inserted {
		return dedupRow.MessageID, true, nil
	}

	enqueueReq := persistence.EnqueueMessageRequest{
		ID:         candidateID,
		InstanceID: instanceID,
		Type:       emitMessageType,
		Sender:     sender,
		SenderKind: senderKind,
		Payload:    body,
	}
	if err := EnqueueMessage(ctx, tx, tables.Messages(), enqueueReq); err != nil {
		return shared.UUID{}, false, fmt.Errorf("emitCascadeMessageInTx: insert envelope: %w", err)
	}
	// @story: cascade-emit
	// @story: cross-frame-coupling
	// @story: one-message-per-frame
	if _, err := frame.EnqueueFrame(ctx, tables, tx, instanceID, candidateID); err != nil {
		return shared.UUID{}, false, fmt.Errorf("emitCascadeMessageInTx: enqueue frame: %w", err)
	}
	return candidateID, false, nil
}
