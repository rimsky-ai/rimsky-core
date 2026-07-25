// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package locks

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/internal/namedreg"
)

type Registry struct {
	reg                    namedreg.Registry[ClaimProducer]
	lookupInstanceBindings func(ctx context.Context, instanceID string) (map[string]json.RawMessage, bool, error)
	lateBindServiceProxies map[string]string
}

type Option func(*Registry)

func WithLookupInstanceBindings(fn func(ctx context.Context, instanceID string) (map[string]json.RawMessage, bool, error)) Option {
	return func(r *Registry) { r.lookupInstanceBindings = fn }
}

func WithLateBindServiceProxies(m map[string]string) Option {
	return func(r *Registry) { r.lateBindServiceProxies = m }
}

func NewRegistry(opts ...Option) *Registry {
	r := &Registry{reg: namedreg.New[ClaimProducer]()}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func (r *Registry) Add(name string, p ClaimProducer) {
	if p != nil {
		if got := p.Name(); got != "" && got != name {
			slog.Warn("producer registry: registration name disagrees with ClaimProducer.Name()",
				"registration_name", name,
				"producer_internal_name", got,
				"hint", "registration name and producer-internal name should agree; check the wiring path that constructed this producer")
		}
	}
	r.reg.Add(name, p)
}

func (r *Registry) Get(name string) (ClaimProducer, bool) {
	return r.reg.Get(name)
}

func (r *Registry) GetWithContext(ctx context.Context, name string, instanceID string) (ClaimProducer, bool) {
	if p, ok := r.Get(name); ok {
		return p, true
	}
	if instanceID == "" {
		return nil, false
	}
	if r.lookupInstanceBindings == nil {
		return nil, false
	}
	proxyName, ok := r.lateBindServiceProxies["claim_producer"]
	if !ok || proxyName == "" {
		return nil, false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	bindings, ok, err := r.lookupInstanceBindings(ctx, instanceID)
	if err != nil {
		slog.Warn("producer registry: instance-bindings lookup failed; classifying as unknown claim producer",
			"instance_id", instanceID,
			"producer_name", name,
			"error", err.Error())
		return nil, false
	}
	if !ok {
		return nil, false
	}
	if _, exists := bindings[name]; !exists {
		return nil, false
	}
	return r.Get(proxyName)
}

func (r *Registry) Producers() map[string]ClaimProducer {
	return r.reg.CopyMap()
}

func (r *Registry) Names() []string {
	return r.reg.Names()
}

func (r *Registry) Close() {
	r.reg.CloseAll()
}
