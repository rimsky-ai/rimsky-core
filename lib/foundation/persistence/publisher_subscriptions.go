// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

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

type PublisherSubscriptionTable interface {
	Insert(ctx context.Context, row PublisherSubscriptionRow, tx Tx) error

	Delete(ctx context.Context, id shared.UUID, tx Tx) error

	ListByInstance(ctx context.Context, instanceID shared.UUID) ([]PublisherSubscriptionRow, error)

	ListByState(ctx context.Context, state string) ([]PublisherSubscriptionRow, error)

	CompareAndSetState(ctx context.Context, id shared.UUID, from, to, failureReason string) (bool, error)

	Get(ctx context.Context, id shared.UUID, tx Tx) (*PublisherSubscriptionRow, error)
}
