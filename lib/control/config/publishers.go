// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// publishers.go — operator-side Publisher + Validation + DataProcessing
// registry assembly. Peers declared in `publishers:` / `claim_producers:`
// / `executors:` may advertise the `publisher`, `validation`, or
// `data_processing` protocol in their `protocols:` list; this file
// dials the appropriate gRPC client per advertised protocol and exposes
// the registries the control-api / supervisor wires into runtime.AppDeps.
//
// Per spec
// .ok-planner/specs/2026-05-17-sensor-messaging-unification-design.md
// §Publisher protocol unification. The registry types are tiny
// adapters around `map[string]<Client>` so the control-api can pass
// them as runtime.PublisherRegistry / runtime.ValidationRegistry
// without coupling to control/config.

package config

import (
	"context"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	peer "github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

// publisherRegistryImpl satisfies runtime.PublisherRegistry over a
// static map populated at startup.
type publisherRegistryImpl struct {
	clients map[string]runtime.PublisherClient
}

func (r *publisherRegistryImpl) Get(name string) (runtime.PublisherClient, bool) {
	c, ok := r.clients[name]
	return c, ok
}

func (r *publisherRegistryImpl) All() []runtime.PublisherClient {
	out := make([]runtime.PublisherClient, 0, len(r.clients))
	for _, c := range r.clients {
		out = append(out, c)
	}
	return out
}

// validationRegistryImpl satisfies runtime.ValidationRegistry over a
// static map populated at startup.
type validationRegistryImpl struct {
	clients map[string]runtime.ValidationClient
}

func (r *validationRegistryImpl) Get(name string) (runtime.ValidationClient, bool) {
	c, ok := r.clients[name]
	return c, ok
}

// dataProcessingRegistryImpl satisfies runtime.DataProcessingRegistry.
type dataProcessingRegistryImpl struct {
	clients map[string]runtime.DataProcessingClient
}

func (r *dataProcessingRegistryImpl) Get(name string) (runtime.DataProcessingClient, bool) {
	c, ok := r.clients[name]
	return c, ok
}

// DialPublisherAndValidationRegistries walks the union of claim_producers
// + executors + publishers. For each peer whose `protocols:` list
// declares `publisher`, `validation`, or `data_processing`, dials the
// matching gRPC client and adds it to the corresponding registry.
// Returns non-nil registries even when no peers advertise the protocol
// — the control-api treats nil entries identically to an empty registry
// (downstream `Get` returns ok=false).
//
// Per-peer dial errors fail startup so the operator notices
// misconfiguration immediately. Each dial is bounded by
// capabilitiesHandshakeTimeout (same envelope as dialRemoteStores).
//
// Closers walks every dialed client and closes its connection; the
// caller invokes this from the shutdown path alongside
// `Registry.Close()` and `LifecycleRegistry.Close()`.
//
// Per the 2026-05-17 unification, publishers live in the new top-level
// `publishers:` block of rimsky.yml. Multi-protocol peers (a peer that
// implements both `publisher` and `validation`, for example) appear
// once per role block — the dial loop walks the unioned peer set and
// dispatches per advertised protocol.
func DialPublisherAndValidationRegistries(
	ctx context.Context, stores RemoteStoresConfig, execs ExecutorsConfig, publishers RemotePublishersConfig,
) (
	publisherReg runtime.PublisherRegistry,
	validators runtime.ValidationRegistry,
	dataProcessors runtime.DataProcessingRegistry,
	closers []func(),
	err error,
) {
	publisherClients := map[string]runtime.PublisherClient{}
	validationClients := map[string]runtime.ValidationClient{}
	dpClients := map[string]runtime.DataProcessingClient{}
	// closeAll captures the three client maps by reference. They start
	// empty and are populated during the dial loop below; at every
	// invocation (mid-loop dial-error rollback OR end-of-function
	// closer registration) closeAll walks whatever entries have
	// accumulated by that point.
	closeAll := func() {
		for _, c := range publisherClients {
			if closer, ok := c.(interface{ Close() }); ok {
				closer.Close()
			}
		}
		for _, c := range validationClients {
			if closer, ok := c.(interface{ Close() }); ok {
				closer.Close()
			}
		}
		for _, c := range dpClients {
			if closer, ok := c.(interface{ Close() }); ok {
				closer.Close()
			}
		}
	}

	// Walk producers + executors + publishers uniformly. The endpoint
	// shape is peer-agnostic — the gRPC dial only cares about the
	// transport target.
	//
	// `roles` is the LIVE `validation_supported_roles` list the peer
	// advertised on its primary protocol's Capabilities handshake. We
	// cannot read it from the operator-declared `e.Capabilities` —
	// `cfg.Stores` is built at YAML-load time and `Capabilities` there
	// carries only the operator-declared write-semantics envelope; the
	// `ValidationSupportedRoles` field is always empty there. So when a
	// claim-producer peer advertises the `validation` mix-in, we run a
	// fresh ClaimProducer.Capabilities handshake here to learn the live
	// supported roles. The cost is one extra RPC per validation-mix-in
	// peer at startup — the same RPC `dialRemoteStores` already runs once,
	// but it doesn't persist the live caps back into `cfg.Stores`, so
	// re-running it here is the path that doesn't require a wider refactor.
	// @story: validation-author
	type peerSpec struct {
		name      string
		endpoint  string
		protocols []string
		// fetchRoles, when non-nil, is called lazily the first time the
		// validation mix-in arm is processed for this peer. Non-store
		// peers (executors / publishers) leave it nil — they advertise
		// validation through different proto Capabilities surfaces that
		// the existing dial code does not yet read; today their
		// supported_roles list is treated as empty (the same pre-fix
		// behavior).
		fetchRoles func(context.Context) ([]string, error)
	}
	peers := make([]peerSpec, 0, len(stores.Stores)+len(execs.Executors)+len(publishers.Publishers))
	for n, e := range stores.Stores {
		nameCopy, endpointCopy := n, e.Endpoint
		peers = append(peers, peerSpec{
			name:      n,
			endpoint:  e.Endpoint,
			protocols: e.Protocols,
			fetchRoles: func(fctx context.Context) ([]string, error) {
				c, dErr := peer.Dial(fctx, nameCopy, endpointCopy)
				if dErr != nil {
					return nil, dErr
				}
				defer c.Close()
				caps, cErr := c.Capabilities(fctx)
				if cErr != nil {
					return nil, cErr
				}
				return append([]string(nil), caps.ValidationSupportedRoles...), nil
			},
		})
	}
	for n, e := range execs.Executors {
		peers = append(peers, peerSpec{name: n, endpoint: e.Endpoint, protocols: e.Protocols})
	}
	for n, e := range publishers.Publishers {
		peers = append(peers, peerSpec{name: n, endpoint: e.Endpoint, protocols: e.Protocols})
	}
	for _, p := range peers {
		for _, proto := range p.protocols {
			switch proto {
			case ProtocolPublisher:
				if _, already := publisherClients[p.name]; already {
					continue
				}
				dialCtx, cancel := context.WithTimeout(ctx, capabilitiesHandshakeTimeout)
				c, dErr := peer.DialPublisher(dialCtx, p.name, p.endpoint)
				cancel()
				if dErr != nil {
					closeAll()
					return nil, nil, nil, nil, fmt.Errorf("DialPublisherAndValidationRegistries: publisher %q: %w", p.name, dErr)
				}
				publisherClients[p.name] = c
			case claimproducer.ProtocolValidation:
				if _, already := validationClients[p.name]; already {
					continue
				}
				// Resolve the LIVE supported_roles list right before
				// dialing the validation client. For claim-producer
				// peers the resolver re-runs the Capabilities handshake;
				// for executors / publishers there is no fetcher today
				// (their primary-protocol caps surfaces do not yet plumb
				// validation_supported_roles into this function), so
				// roles stays nil.
				var roles []string
				if p.fetchRoles != nil {
					rCtx, rCancel := context.WithTimeout(ctx, capabilitiesHandshakeTimeout)
					rolesResolved, fErr := p.fetchRoles(rCtx)
					rCancel()
					if fErr != nil {
						closeAll()
						return nil, nil, nil, nil, fmt.Errorf("DialPublisherAndValidationRegistries: validation %q: resolve supported_roles: %w", p.name, fErr)
					}
					roles = rolesResolved
				}
				dialCtx, cancel := context.WithTimeout(ctx, capabilitiesHandshakeTimeout)
				c, dErr := peer.DialValidation(dialCtx, p.name, p.endpoint, roles)
				cancel()
				if dErr != nil {
					closeAll()
					return nil, nil, nil, nil, fmt.Errorf("DialPublisherAndValidationRegistries: validation %q: %w", p.name, dErr)
				}
				validationClients[p.name] = c
			case claimproducer.ProtocolDataProcessing:
				if _, already := dpClients[p.name]; already {
					continue
				}
				dialCtx, cancel := context.WithTimeout(ctx, capabilitiesHandshakeTimeout)
				c, dErr := peer.DialDataProcessing(dialCtx, p.name, p.endpoint)
				cancel()
				if dErr != nil {
					closeAll()
					return nil, nil, nil, nil, fmt.Errorf("DialPublisherAndValidationRegistries: data_processing %q: %w", p.name, dErr)
				}
				dpClients[p.name] = c
			}
		}
	}
	closers = append(closers, closeAll)
	return &publisherRegistryImpl{clients: publisherClients},
		&validationRegistryImpl{clients: validationClients},
		&dataProcessingRegistryImpl{clients: dpClients},
		closers, nil
}
