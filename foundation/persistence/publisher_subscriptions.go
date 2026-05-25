// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// publisher_subscriptions.go — per-row-type Table accessor for
// table:rimsky_publisher_subscriptions.
//
// A publisher_subscription is one publisher peer's commitment to publish
// messages for one instance. Lifecycle is managed by control-api's
// instance-create / instance-terminate paths and reconciled at supervisor
// startup via runtime.ResyncPublisherSubscriptions.
//
// @concept: publisher-subscription
package persistence

import (
	"context"
	"encoding/json"
	"time"

	"github.com/fallguyconsulting/rimsky/foundation/shared"
)

// PublisherSubscriptionState values for col:rimsky_publisher_subscriptions.state.
const (
	PublisherSubscriptionStateActive  = "active"
	PublisherSubscriptionStateFailed  = "failed"
	PublisherSubscriptionStateStopped = "stopped"
)

// PublisherSubscriptionRow is the per-row representation of
// table:rimsky_publisher_subscriptions. One row per (instance, publisher)
// declared in the template's `publishers:` block.
type PublisherSubscriptionRow struct {
	ID             shared.UUID
	InstanceID     shared.UUID
	PublisherName  string
	Kind           string
	ResolvedConfig json.RawMessage
	TargetNode     string
	MessageKind    string
	StartedAt      time.Time
	State          string
}

// PublisherSubscriptionUpdate is the partial-update payload for
// PublisherSubscriptionsTable.Update.
type PublisherSubscriptionUpdate struct {
	State     *string
	StartedAt *time.Time
}

// PublisherSubscriptionsTable is the per-row-type Table accessor for
// table:rimsky_publisher_subscriptions.
type PublisherSubscriptionsTable interface {
	// Insert creates a new publisher-subscription row. Called by
	// control-api at instance create after the publisher's Subscribe RPC
	// returns OK.
	Insert(ctx context.Context, tx Tx, row PublisherSubscriptionRow) error

	// Update applies a partial update to a publisher-subscription row.
	Update(ctx context.Context, tx Tx, id shared.UUID, upd PublisherSubscriptionUpdate) error

	// Delete removes a publisher-subscription row. Called at instance
	// termination after Unsubscribe returns OK.
	Delete(ctx context.Context, tx Tx, id shared.UUID) error

	// ListByInstance returns all publisher-subscriptions for an instance.
	ListByInstance(ctx context.Context, instanceID shared.UUID) ([]PublisherSubscriptionRow, error)

	// ListByState returns publisher-subscriptions in a given state across
	// all instances. Used by the resync sweeper.
	ListByState(ctx context.Context, state string) ([]PublisherSubscriptionRow, error)

	// Get returns one publisher-subscription by id, or nil when absent.
	// Pass tx to route the read through the surrounding transaction (used
	// by the message-create handler so the capability check sees the same
	// snapshot as the insert). Pass nil for non-tx pool reads.
	Get(ctx context.Context, tx Tx, id shared.UUID) (*PublisherSubscriptionRow, error)
}
