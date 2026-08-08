// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package clientiface

import (
	"context"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

type PublisherClient interface {
	Name() string

	// @concept: publisher
	SupportedKinds(ctx context.Context) ([]string, error)

	Subscribe(ctx context.Context, req SubscribeRequest) error

	Unsubscribe(ctx context.Context, subscriptionID shared.UUID) error

	ListSubscriptions(ctx context.Context) ([]ListedPublisherSubscription, error)
}

type SubscribeRequest struct {
	PublisherSubscriptionID shared.UUID
	InstanceID              shared.UUID
	Kind                    string
	ResolvedConfig          []byte
	MessageType             string
}

type ListedPublisherSubscription struct {
	PublisherSubscriptionID shared.UUID
	InstanceID              shared.UUID
	Kind                    string
	MessageType             string
}

type PublisherRegistry interface {
	Get(name string) (PublisherClient, bool)
	All() []PublisherClient
}
