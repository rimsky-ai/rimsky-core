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
	// @constraint: closeAll captures the three client maps by reference
	// — they start empty and accumulate during the dial loop below; at
	// every invocation (mid-loop dial-error rollback OR end-of-function
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

	// @deliberate: walk producers + executors + publishers uniformly —
	// the endpoint shape is peer-agnostic; the gRPC dial only cares about
	// the transport target. `roles` is the LIVE
	// `validation_supported_roles` list the peer advertised on its own
	// capability surface — the Validation service has no Capabilities
	// verb, so each peer kind carries the list on its host capability
	// handshake: ClaimProducer.Capabilities for claim producers,
	// ExecutorObservability.Capabilities for executors,
	// Publisher.Capabilities for publishers. We cannot read it from the
	// operator-declared `e.Capabilities` — `cfg.Stores` is built at
	// YAML-load time and `Capabilities` there carries only the
	// operator-declared write-semantics envelope; the
	// `ValidationSupportedRoles` field is always empty there. So when a
	// peer advertises the `validation` mix-in, we run a fresh capability
	// handshake here to learn the live supported roles. The cost is one
	// extra RPC per validation-mix-in peer at startup.
	// @story: validation-author
	// @story: validation-mixin-uniform
	type peerSpec struct {
		name     string
		endpoint string
		// @constraint: tls is the validated `tls:` mode from the peer's
		// config entry.
		tls       string
		protocols []string
		// @deliberate: fetchRoles is called lazily the first time the
		// validation mix-in arm is processed for this peer. Every peer
		// kind sets it — all three kinds resolve live roles identically.
		fetchRoles func(context.Context) ([]string, error)
	}
	peers := make([]peerSpec, 0, len(stores.Stores)+len(execs.Executors)+len(publishers.Publishers))
	for n, e := range stores.Stores {
		nameCopy, endpointCopy, tlsCopy := n, e.Endpoint, e.TLS
		peers = append(peers, peerSpec{
			name:      n,
			endpoint:  e.Endpoint,
			tls:       e.TLS,
			protocols: e.Protocols,
			fetchRoles: func(fctx context.Context) ([]string, error) {
				c, dErr := peer.Dial(fctx, nameCopy, endpointCopy, tlsCopy)
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
		// @constraint: the executor's validation_supported_roles ride on
		// the ExecutorObservability capability surface, which the
		// operator may split onto a dedicated observability endpoint.
		nameCopy, tlsCopy := n, e.TLS
		obsEndpoint := e.ObservabilityEndpoint
		if obsEndpoint == "" {
			obsEndpoint = e.Endpoint
		}
		peers = append(peers, peerSpec{
			name:      n,
			endpoint:  e.Endpoint,
			tls:       e.TLS,
			protocols: e.Protocols,
			fetchRoles: func(fctx context.Context) ([]string, error) {
				return peer.FetchExecutorValidationRoles(fctx, nameCopy, obsEndpoint, tlsCopy)
			},
		})
	}
	for n, e := range publishers.Publishers {
		nameCopy, endpointCopy, tlsCopy := n, e.Endpoint, e.TLS
		peers = append(peers, peerSpec{
			name:      n,
			endpoint:  e.Endpoint,
			tls:       e.TLS,
			protocols: e.Protocols,
			fetchRoles: func(fctx context.Context) ([]string, error) {
				return peer.FetchPublisherValidationRoles(fctx, nameCopy, endpointCopy, tlsCopy)
			},
		})
	}
	for _, p := range peers {
		for _, proto := range p.protocols {
			switch proto {
			case ProtocolPublisher:
				if _, already := publisherClients[p.name]; already {
					continue
				}
				dialCtx, cancel := context.WithTimeout(ctx, capabilitiesHandshakeTimeout)
				c, dErr := peer.DialPublisher(dialCtx, p.name, p.endpoint, p.tls)
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
				// @constraint: resolve the LIVE supported_roles list
				// right before dialing the validation client. Every peer
				// kind runs its own capability handshake (ClaimProducer /
				// ExecutorObservability / Publisher Capabilities), so all
				// three kinds resolve live roles identically. A failed
				// handshake fails startup — a validation peer whose roles
				// cannot be learned would be dialed but never used, which
				// is exactly the silent gap this guards against.
				rCtx, rCancel := context.WithTimeout(ctx, capabilitiesHandshakeTimeout)
				roles, fErr := p.fetchRoles(rCtx)
				rCancel()
				if fErr != nil {
					closeAll()
					return nil, nil, nil, nil, fmt.Errorf("DialPublisherAndValidationRegistries: validation %q: resolve supported_roles: %w", p.name, fErr)
				}
				dialCtx, cancel := context.WithTimeout(ctx, capabilitiesHandshakeTimeout)
				c, dErr := peer.DialValidation(dialCtx, p.name, p.endpoint, p.tls, roles)
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
				c, dErr := peer.DialDataProcessing(dialCtx, p.name, p.endpoint, p.tls)
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
