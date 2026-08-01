// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: service
// @concept: discovery-cache
package config

import (
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/control/observability"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	protocol "github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	rtclaimproducer "github.com/rimsky-ai/rimsky-core/lib/runtime/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
)

type BundledExecutorAdvertisement struct {
	Schema       []byte
	Tags         []string
	ErrorClasses []string
}

type BundledRegistrations struct {
	ExecutorHandlers      map[string]executor.InProcessHandler
	ExecutorAliases       map[string]executor.Endpoint
	ExecutorAdverts       map[string]BundledExecutorAdvertisement
	ClaimProducerAdverts  map[string]protocol.Capabilities
	ClaimProducerRegistry *rtclaimproducer.InProcessRegistry
}

func mergeBundledClaimProducers(registry *locks.Registry, bundled map[string]locks.ClaimProducer, onOverride func(name string)) {
	for name, producer := range bundled {
		if _, exists := registry.Get(name); exists {
			if onOverride != nil {
				onOverride(name)
			}
			continue
		}
		registry.Add(name, producer)
	}
}

func (b *BundledRegistrations) ClaimProducerClients() map[string]locks.ClaimProducer {
	out := map[string]locks.ClaimProducer{}
	if b.ClaimProducerRegistry == nil {
		return out
	}
	for _, name := range b.ClaimProducerRegistry.RegisteredNames() {
		if c, ok := b.ClaimProducerRegistry.Client(name); ok {
			out[name] = c
		}
	}
	return out
}

// @decision: bundled-executor-inproc-capability-advertisement
func (b *BundledRegistrations) AdvertiseInto(disc *observability.Discovery, configuredExecutors map[string]ExecutorEntry, configuredClaimProducers map[string]ClaimProducerEntry) {
	now := time.Now()
	for name, adv := range b.ExecutorAdverts {
		if _, overridden := configuredExecutors[name]; overridden {
			continue
		}
		disc.SetExecutor(observability.PeerEntry{
			Name:         name,
			Endpoint:     b.ExecutorAliases[name].URL,
			Reachability: observability.ReachabilityReachable,
			Static:       true,
			LastProbedAt: now,
			Capabilities: &observability.ObservabilityCapabilities{
				ExpectedAttributesSchema: adv.Schema,
				DeclaredTags:             adv.Tags,
				DeclaredErrorClasses:     adv.ErrorClasses,
			},
		})
	}
	for name, caps := range b.ClaimProducerAdverts {
		if _, overridden := configuredClaimProducers[name]; overridden {
			continue
		}
		disc.SetClaimProducer(observability.PeerEntry{
			Name:         name,
			Reachability: observability.ReachabilityReachable,
			Static:       true,
			LastProbedAt: now,
			Capabilities: &observability.ObservabilityCapabilities{
				DeclaredErrorClasses: caps.DeclaredErrorClasses,
			},
		})
	}
}
