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

// @constraint: publisher-subscription state-machine — values for
// col:rimsky_publisher_subscriptions.state. Rows are created in `mounting`
// (desired state, not yet confirmed by the publisher); the reconciliation
// worker flips them to `active` once the publisher Subscribe handshake
// succeeds. `failed` is reserved for non-retryable errors (e.g. an
// unregistered publisher name) and carries a FailureReason. `stopped` is
// the unsubscribe terminal.
const (
	PublisherSubscriptionStateMounting = "mounting"
	PublisherSubscriptionStateActive   = "active"
	PublisherSubscriptionStateFailed   = "failed"
	PublisherSubscriptionStateStopped  = "stopped"
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
	MessageType    string
	StartedAt      time.Time
	State          string
	// FailureReason is the operator-readable explanation for a
	// state='failed' row (empty otherwise). Surfaced on the
	// instance-detail API.
	FailureReason string
}

// PublisherSubscriptionsTable is the per-row-type Table accessor for
// table:rimsky_publisher_subscriptions.
//
// All state transitions go through CompareAndSetState — there is no
// blind partial-update method, so a settled row's state (and a failed
// row's reason) can never be clobbered by a stale writer.
type PublisherSubscriptionsTable interface {
	// @constraint: Insert runs inside the control-api instance-create
	// transaction with state='mounting'; the publisher Subscribe
	// handshake is driven asynchronously by the reconciliation worker,
	// never inline with instance creation.
	Insert(ctx context.Context, tx Tx, row PublisherSubscriptionRow) error

	// @constraint: Delete is called at instance termination only after
	// Unsubscribe returns OK.
	Delete(ctx context.Context, tx Tx, id shared.UUID) error

	ListByInstance(ctx context.Context, instanceID shared.UUID) ([]PublisherSubscriptionRow, error)

	// @agent-contract: ListByState returns publisher-subscriptions in a
	// given state across all instances. Used by the reconciliation
	// worker (mounting rows) and the startup resync sweep
	// (mounting + active rows).
	ListByState(ctx context.Context, state string) ([]PublisherSubscriptionRow, error)

	// @constraint: CompareAndSetState is a single-statement guarded
	// UPDATE — flips a row from `from` to `to` (stamping failureReason —
	// pass "" to clear) only when the row is still in `from`, and
	// reports whether a row was updated. The guard protects the
	// reconciler's mounting→active / mounting→failed flips against
	// concurrent lifecycle transitions (e.g. instance terminate marking
	// the row stopped while a Subscribe RPC is in flight) — a settled
	// row is never overwritten by a late flip.
	CompareAndSetState(ctx context.Context, id shared.UUID, from, to, failureReason string) (bool, error)

	// @constraint: Get requires a non-nil tx (the no-nil-tx contract —
	// wrap with Tables.Transaction); the message-create handler routes
	// the read through its surrounding transaction so the capability
	// check sees the same snapshot as the insert. Returns nil when
	// absent.
	Get(ctx context.Context, tx Tx, id shared.UUID) (*PublisherSubscriptionRow, error)
}
