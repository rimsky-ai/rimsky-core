// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @decision: service-delivery-stall-signal
// @concept: event-log

package persistence

import (
	"context"
	"time"
)

type ServiceDeliveryOutbox string

const (
	ServiceDeliveryOutboxLifecycle    ServiceDeliveryOutbox = "lifecycle"
	ServiceDeliveryOutboxProducerVerb ServiceDeliveryOutbox = "producer_verb"
)

// @decision: service-delivery-stall-signal
const DefaultServiceOutboxPageSize = 100

// @decision: service-delivery-stall-signal
type ServiceDeliveryStallTable interface {
	MarkStalled(ctx context.Context, service string, outbox ServiceDeliveryOutbox, since time.Time, tx Tx) (bool, error)
	ClearStalled(ctx context.Context, service string, outbox ServiceDeliveryOutbox, tx Tx) (bool, error)
	ListStalled(ctx context.Context, outbox ServiceDeliveryOutbox, tx Tx) ([]string, error)
}
