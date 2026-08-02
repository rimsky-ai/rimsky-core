// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: message-sender-node
// @concept: message

package runtime

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// @story: cascade-send
// @concept: message-sender-node
// @decision: intx-suffix-convention
func sendCascadeMessage(
	ctx context.Context,
	tables persistence.Tables,
	instanceID, nodeID, frameID shared.UUID,
	sendMessageType string,
	body []byte,
	tx persistence.Tx,
) (shared.UUID, bool, error) {
	if tx != nil {
		return sendCascadeMessageRow(ctx, tables, instanceID, nodeID, frameID, sendMessageType, body, tx)
	}
	var messageID shared.UUID
	var replayed bool
	err := tables.Transaction(ctx, func(ctx context.Context, tx persistence.Tx) error {
		var ierr error
		messageID, replayed, ierr = sendCascadeMessageRow(ctx, tables, instanceID, nodeID, frameID, sendMessageType, body, tx)
		return ierr
	})
	return messageID, replayed, err
}

func sendCascadeMessageRow(
	ctx context.Context, tables persistence.Tables, instanceID, nodeID, frameID shared.UUID, sendMessageType string, body []byte, tx persistence.Tx,
) (shared.UUID, bool, error) {
	if instanceID == (shared.UUID{}) {
		return shared.UUID{}, false, fmt.Errorf("sendCascadeMessage: instance_id required")
	}
	if nodeID == (shared.UUID{}) {
		return shared.UUID{}, false, fmt.Errorf("sendCascadeMessage: node_id required")
	}
	if frameID == (shared.UUID{}) {
		return shared.UUID{}, false, fmt.Errorf("sendCascadeMessage: frame_id required")
	}
	if sendMessageType == "" {
		return shared.UUID{}, false, fmt.Errorf("sendCascadeMessage: sends_message type required")
	}
	if len(body) == 0 {
		body = []byte(`{}`)
	}

	idempotencyKey := fmt.Sprintf("cascade-send:%s:%s", nodeID.String(), frameID.String())
	senderKind := SenderKindInstance
	sender := "instance:" + instanceID.String()

	candidateID := shared.UUID(uuid.New())

	dedupRow, inserted, err := tables.MessageIdempotencies().InsertOrLookup(ctx, persistence.MessageIdempotencyRow{
		InstanceID:     instanceID,
		SenderKind:     senderKind,
		Sender:         sender,
		SenderSubject:  "",
		IdempotencyKey: idempotencyKey,
		MessageID:      candidateID,
	}, tx)
	if err != nil {
		return shared.UUID{}, false, fmt.Errorf("sendCascadeMessage: idempotency upsert: %w", err)
	}
	if !inserted {
		return dedupRow.MessageID, true, nil
	}

	enqueueReq := persistence.EnqueueMessageRequest{
		ID:         candidateID,
		InstanceID: instanceID,
		Type:       sendMessageType,
		Sender:     sender,
		SenderKind: senderKind,
		Payload:    body,
	}
	if err := EnqueueMessage(ctx, tables, enqueueReq, tx); err != nil {
		return shared.UUID{}, false, fmt.Errorf("sendCascadeMessage: insert envelope: %w", err)
	}
	return candidateID, false, nil
}
