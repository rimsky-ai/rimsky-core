// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package config

import (
	"context"
	"fmt"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	peer "github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

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

type validationRegistryImpl struct {
	clients map[string]runtime.ValidationClient
}

func (r *validationRegistryImpl) Get(name string) (runtime.ValidationClient, bool) {
	c, ok := r.clients[name]
	return c, ok
}

type dataProcessingRegistryImpl struct {
	clients map[string]runtime.DataProcessingClient
}

func (r *dataProcessingRegistryImpl) Get(name string) (runtime.DataProcessingClient, bool) {
	c, ok := r.clients[name]
	return c, ok
}

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

	// @story: validation-author
	// @story: validation-mixin-uniform
	type peerSpec struct {
		name     string
		endpoint string
		tls       string
		protocols []string
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
