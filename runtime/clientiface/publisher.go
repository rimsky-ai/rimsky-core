// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// publisher.go — rimsky-side wire-shape types for the Publisher
// protocol. See package doc in `data_processing.go` for the licensing-
// boundary rationale (separate Apache package so the gRPC remote
// client and conformance binaries can link against the interface).

package clientiface

import (
	"context"

	"github.com/fallguyconsulting/rimsky/foundation/shared"
)

// PublisherClient is the rimsky-side surface every publisher-service
// binding satisfies. Implementations live in
// `runtime/peer/publisher_client.go` (the gRPC client) or in test
// fixtures.
type PublisherClient interface {
	Name() string

	// Subscribe begins a publisher-subscription on the publisher service.
	// The subscription_id is rimsky-generated UUIDv4; the publisher binds
	// it internally.
	Subscribe(ctx context.Context, req SubscribeRequest) error

	// Unsubscribe tears down a previously-started publisher-subscription.
	Unsubscribe(ctx context.Context, subscriptionID shared.UUID) error

	// ListSubscriptions enumerates the publisher-subscriptions the
	// publisher currently has. Used by `ResyncPublisherSubscriptions` to
	// reconcile state after rimsky (or the publisher) restarts.
	ListSubscriptions(ctx context.Context) ([]ListedPublisherSubscription, error)
}

// SubscribeRequest is the rimsky-side payload for PublisherClient.Subscribe.
// Mirrors the proto SubscribeRequest 1:1 with inline routing fields
// (target_node + message_kind) and `[]byte` config bytes per
// `@blessed-invariant 21` (config is opaque to rimsky once resolved).
type SubscribeRequest struct {
	PublisherSubscriptionID shared.UUID
	InstanceID              shared.UUID
	Kind                    string
	ResolvedConfig          []byte
	TargetNode              string
	MessageKind             string
}

// ListedPublisherSubscription is the rimsky-side projection of the
// proto PublisherSubscriptionDescriptor returned by
// Publisher.ListSubscriptions.
type ListedPublisherSubscription struct {
	PublisherSubscriptionID shared.UUID
	InstanceID              shared.UUID
	Kind                    string
	TargetNode              string
	MessageKind             string
}

// PublisherRegistry resolves a publisher name (as declared in
// `rimsky.yml`) to the corresponding PublisherClient. Returns ok=false
// when the named publisher is not configured on this process.
type PublisherRegistry interface {
	Get(name string) (PublisherClient, bool)
	// All returns every registered PublisherClient. Used by the resync
	// sweeper, which fans out across the full set.
	All() []PublisherClient
}
