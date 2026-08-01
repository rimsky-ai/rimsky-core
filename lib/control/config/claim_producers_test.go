// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package config

import (
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

func TestDialLifecycleSubscribers_DialsLifecycleDeclaringPublishers(t *testing.T) {
	publishers := RemotePublishersConfig{
		Publishers: map[string]PublisherEntry{
			"pub-with-lifecycle": {
				Endpoint:  "grpc://127.0.0.1:1",
				Protocols: []string{ProtocolPublisher, claimproducer.ProtocolLifecycleSubscriber},
			},
			"pub-without-lifecycle": {
				Endpoint:  "grpc://127.0.0.1:1",
				Protocols: []string{ProtocolPublisher},
			},
		},
	}

	reg, err := DialLifecycleSubscribers(context.Background(), RemoteClaimProducersConfig{}, ExecutorsConfig{}, publishers)
	if err != nil {
		t.Fatalf("DialLifecycleSubscribers: %v", err)
	}
	t.Cleanup(reg.Close)

	if _, ok := reg.Get("pub-with-lifecycle"); !ok {
		t.Fatalf("publisher declaring lifecycle_subscriber must be dialed into the lifecycle registry")
	}
	if _, ok := reg.Get("pub-without-lifecycle"); ok {
		t.Fatalf("publisher not declaring lifecycle_subscriber must not be dialed into the lifecycle registry")
	}
}
