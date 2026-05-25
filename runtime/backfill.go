// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// backfill.go — E15. Backfill operation handling.
//
// Spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Backfills. A backfill is an operator-initiated invalidate-class
// message carrying a `backfill_operation_id` and an optional
// partition_request override. The message routes to a fan-out node's
// `partition_request` substitution; the node re-fires on the next
// frame with the supplied partition shape.
//
// @concept: backfill
// @concept: message
//
// In-flight frames complete normally; preemption is V2.

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/fallguyconsulting/rimsky/foundation/persistence"
	"github.com/fallguyconsulting/rimsky/foundation/shared"
)

// BackfillCreateRequest is the input to CreateBackfill. Spec
// §Backfills / `POST /instances/{id}/backfills`.
type BackfillCreateRequest struct {
	InstanceID               shared.UUID
	TargetNode               string
	PartitionRequestOverride json.RawMessage // opaque to rimsky
	Reason                   string
	Sender                   string // operator identity
}

// BackfillCreated is the return shape carrying both the message id and
// the operation id so the operator can poll status.
type BackfillCreated struct {
	MessageID           shared.UUID
	BackfillOperationID shared.UUID
}

// CreateBackfill enqueues an invalidate-class message representing a
// backfill operation. The runtime layer fires this from the
// `POST /instances/{id}/backfills` handler.
//
// `target_node` is the alias of the fan-out node to backfill; rimsky
// does not validate the alias here (the control-api layer performs the
// template-lookup validation before invoking).
func CreateBackfill(
	ctx context.Context, tx persistence.Tx, m persistence.MessagesTable,
	now time.Time, req BackfillCreateRequest,
) (BackfillCreated, error) {
	if req.InstanceID == (shared.UUID{}) {
		return BackfillCreated{}, errors.New("CreateBackfill: instance_id required")
	}
	if req.TargetNode == "" {
		return BackfillCreated{}, errors.New("CreateBackfill: target_node required")
	}
	if req.Sender == "" {
		req.Sender = "operator"
	}
	opID := shared.UUID(uuid.New())
	msgID := shared.UUID(uuid.New())
	// The payload carries the override + the op id + the reason. Rimsky
	// does not interpret these bytes (`@blessed-invariant 21`); the
	// fan-out node's `partition_request` substitution reads named
	// fields by walkPath only.
	payload := map[string]any{
		"backfill_operation_id":      opID.String(),
		"partition_request_override": json.RawMessage(req.PartitionRequestOverride),
		"reason":                     req.Reason,
	}
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return BackfillCreated{}, fmt.Errorf("CreateBackfill: marshal payload: %w", err)
	}
	enqueueReq := persistence.EnqueueMessageRequest{
		ID:                  msgID,
		InstanceID:          req.InstanceID,
		Kind:                "invalidate",
		Sender:              req.Sender,
		SenderKind:          "operator",
		Target:              req.TargetNode,
		Payload:             rawPayload,
		BackfillOperationID: &opID,
		ReceivedAt:          now,
	}
	if err := EnqueueMessage(ctx, tx, m, enqueueReq); err != nil {
		return BackfillCreated{}, fmt.Errorf("CreateBackfill: enqueue: %w", err)
	}
	return BackfillCreated{MessageID: msgID, BackfillOperationID: opID}, nil
}

// CancelBackfill marks every pending message bound to `opID` cancelled
// (sets `col:rimsky_messages.cancelled = TRUE`, `delivered_at = now()`,
// `frame_id = NULL`). In-flight frames complete normally.
//
// Returns the number of message rows affected; zero means the op
// already delivered or was never created.
func CancelBackfill(
	ctx context.Context, tx persistence.Tx, m persistence.MessagesTable,
	now time.Time, opID shared.UUID,
) (int, error) {
	if opID == (shared.UUID{}) {
		return 0, errors.New("CancelBackfill: op_id required")
	}
	return m.MarkCancelled(ctx, tx, opID, now)
}

// BackfillStatus is the return shape for the `GET /backfills/{op_id}`
// surface. Spec §Backfills / Status.
type BackfillStatus struct {
	OperationID shared.UUID
	InstanceID  shared.UUID
	TargetNode  string
	Reason      string
	ReceivedAt  time.Time
	DeliveredAt *time.Time
	FrameID     *shared.UUID
	Cancelled   bool
}

// GetBackfillStatus returns the most recent message bound to `opID`
// resolved into a status snapshot. Used by the operator status endpoint.
// Joining message → frame_id → child runs (the aggregated status of
// the backfill fan-out wave) is the control-api layer's concern; this
// helper resolves the message-side fields only.
func GetBackfillStatus(
	ctx context.Context, tx persistence.Tx, m persistence.MessagesTable,
	opID shared.UUID,
) (*BackfillStatus, error) {
	rows, err := m.List(ctx, persistence.MessageListFilter{
		BackfillOperationID: &opID,
	}, persistence.ListPagination{Limit: 1})
	if err != nil {
		return nil, fmt.Errorf("GetBackfillStatus: %w", err)
	}
	if len(rows.Rows) == 0 {
		return nil, nil
	}
	r := rows.Rows[0]
	status := &BackfillStatus{
		OperationID: opID,
		InstanceID:  r.InstanceID,
		TargetNode:  r.Target,
		ReceivedAt:  r.ReceivedAt,
		DeliveredAt: r.DeliveredAt,
		FrameID:     r.FrameID,
		Cancelled:   r.Cancelled,
	}
	var payload struct {
		Reason string `json:"reason"`
	}
	if len(r.Payload) > 0 {
		_ = json.Unmarshal(r.Payload, &payload)
	}
	status.Reason = payload.Reason
	return status, nil
}
