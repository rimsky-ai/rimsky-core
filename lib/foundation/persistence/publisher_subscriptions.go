// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: publisher-subscription

package persistence

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

const (
	PublisherSubscriptionStateMounting = "mounting"
	PublisherSubscriptionStateActive   = "active"
	PublisherSubscriptionStateFailed   = "failed"
	PublisherSubscriptionStateStopped  = "stopped"
)

type PublisherSubscriptionRow struct {
	ID             shared.UUID
	InstanceID     shared.UUID
	PublisherName  string
	Kind           string
	ResolvedConfig json.RawMessage
	MessageType    string
	StartedAt      time.Time
	State          string
	FailureReason  string
}

type PublisherSubscriptionsTable interface {
	Insert(ctx context.Context, tx Tx, row PublisherSubscriptionRow) error

	Delete(ctx context.Context, tx Tx, id shared.UUID) error

	ListByInstance(ctx context.Context, instanceID shared.UUID) ([]PublisherSubscriptionRow, error)

	ListByState(ctx context.Context, state string) ([]PublisherSubscriptionRow, error)

	CompareAndSetState(ctx context.Context, id shared.UUID, from, to, failureReason string) (bool, error)

	Get(ctx context.Context, tx Tx, id shared.UUID) (*PublisherSubscriptionRow, error)
}
