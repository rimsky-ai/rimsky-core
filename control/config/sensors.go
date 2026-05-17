// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// sensors.go — operator-side Sensor + Validation + DataProcessing
// registry assembly. Peers declared in `claim_producers:` / `executors:`
// may advertise the `sensor`, `validation`, or `data_processing`
// protocol in their `protocols:` list; this file dials the appropriate
// gRPC client per advertised protocol and exposes the registries the
// control-api / supervisor wires into runtime.AppDeps.
//
// Per spec
// .ok-planner/specs/2026-05-15-data-platform-extensions-design.md
// §Protocol surfaces. The registry types are tiny adapters around
// `map[string]<Client>` so the control-api can pass them as
// runtime.SensorRegistry / runtime.ValidationRegistry without coupling
// to control/config.

package config

import (
	"context"
	"fmt"

	"github.com/fallguy/rimsky/runtime"
	"github.com/fallguy/rimsky/runtime/remote"
)

// sensorRegistryImpl satisfies runtime.SensorRegistry over a static map
// populated at startup.
type sensorRegistryImpl struct {
	clients map[string]runtime.SensorClient
}

func (r *sensorRegistryImpl) Get(name string) (runtime.SensorClient, bool) {
	c, ok := r.clients[name]
	return c, ok
}

func (r *sensorRegistryImpl) All() []runtime.SensorClient {
	out := make([]runtime.SensorClient, 0, len(r.clients))
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

// DialSensorAndValidationRegistries walks the union of claim_producers
// + executors. For each peer whose `protocols:` list declares
// `sensor`, `validation`, or `data_processing`, dials the matching
// gRPC client and adds it to the corresponding registry. Returns
// non-nil registries even when no peers advertise the protocol — the
// control-api treats nil entries identically to an empty registry
// (downstream `Get` returns ok=false).
//
// Per-peer dial errors fail startup so the operator notices
// misconfiguration immediately. Each dial is bounded by
// capabilitiesHandshakeTimeout (same envelope as dialRemoteStores).
//
// Closers walks every dialed client and closes its connection; the
// caller invokes this from the shutdown path alongside
// `Registry.Close()` and `LifecycleRegistry.Close()`.
func DialSensorAndValidationRegistries(
	ctx context.Context, stores RemoteStoresConfig, execs ExecutorsConfig,
) (
	sensors runtime.SensorRegistry,
	validators runtime.ValidationRegistry,
	dataProcessors runtime.DataProcessingRegistry,
	closers []func(),
	err error,
) {
	sensorClients := map[string]runtime.SensorClient{}
	validationClients := map[string]runtime.ValidationClient{}
	dpClients := map[string]runtime.DataProcessingClient{}
	// closeAll captures the three client maps by reference. They start
	// empty and are populated during the dial loop below; at every
	// invocation (mid-loop dial-error rollback OR end-of-function
	// closer registration) closeAll walks whatever entries have
	// accumulated by that point. This is intentional — the rollback
	// path on a per-peer dial failure must close every client dialed
	// up to that point, not just the ones declared at function entry.
	closeAll := func() {
		for _, c := range sensorClients {
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

	// Walk producers + executors uniformly. The endpoint shape is
	// peer-agnostic — the gRPC dial only cares about the transport
	// target.
	type peerSpec struct {
		name      string
		endpoint  string
		protocols []string
		roles     []string // executors carry validation_supported_roles in caps
	}
	peers := make([]peerSpec, 0, len(stores.Stores)+len(execs.Executors))
	for n, e := range stores.Stores {
		peers = append(peers, peerSpec{
			name:      n,
			endpoint:  e.Endpoint,
			protocols: e.Protocols,
			roles:     e.Capabilities.ValidationSupportedRoles,
		})
	}
	for n, e := range execs.Executors {
		peers = append(peers, peerSpec{name: n, endpoint: e.Endpoint, protocols: e.Protocols})
	}
	for _, p := range peers {
		for _, proto := range p.protocols {
			switch proto {
			case ProtocolSensor:
				dialCtx, cancel := context.WithTimeout(ctx, capabilitiesHandshakeTimeout)
				c, dErr := remote.DialSensor(dialCtx, p.name, p.endpoint)
				cancel()
				if dErr != nil {
					closeAll()
					return nil, nil, nil, nil, fmt.Errorf("DialSensorAndValidationRegistries: sensor %q: %w", p.name, dErr)
				}
				sensorClients[p.name] = c
			case ProtocolValidation:
				dialCtx, cancel := context.WithTimeout(ctx, capabilitiesHandshakeTimeout)
				c, dErr := remote.DialValidation(dialCtx, p.name, p.endpoint, p.roles)
				cancel()
				if dErr != nil {
					closeAll()
					return nil, nil, nil, nil, fmt.Errorf("DialSensorAndValidationRegistries: validation %q: %w", p.name, dErr)
				}
				validationClients[p.name] = c
			case ProtocolDataProcessing:
				dialCtx, cancel := context.WithTimeout(ctx, capabilitiesHandshakeTimeout)
				c, dErr := remote.DialDataProcessing(dialCtx, p.name, p.endpoint)
				cancel()
				if dErr != nil {
					closeAll()
					return nil, nil, nil, nil, fmt.Errorf("DialSensorAndValidationRegistries: data_processing %q: %w", p.name, dErr)
				}
				dpClients[p.name] = c
			}
		}
	}
	closers = append(closers, closeAll)
	return &sensorRegistryImpl{clients: sensorClients},
		&validationRegistryImpl{clients: validationClients},
		&dataProcessingRegistryImpl{clients: dpClients},
		closers, nil
}
