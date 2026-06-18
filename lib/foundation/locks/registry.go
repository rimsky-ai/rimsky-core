// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package locks

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
)

type NamedLockConfig struct {
	Limit int `yaml:"limit"`
}

type NamedLocksConfig struct {
	Locks map[string]NamedLockConfig
}

func (c NamedLocksConfig) Get(name string) (NamedLockConfig, bool) {
	if c.Locks == nil {
		return NamedLockConfig{}, false
	}
	cfg, ok := c.Locks[name]
	return cfg, ok
}

func (c NamedLocksConfig) Validate() error {
	for name, cfg := range c.Locks {
		if cfg.Limit < 1 {
			return fmt.Errorf("named_locks[%q]: limit must be >= 1, got %d", name, cfg.Limit)
		}
	}
	return nil
}

type Registry struct {
	producers              map[string]ClaimProducer
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
	r := &Registry{producers: make(map[string]ClaimProducer)}
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
	r.producers[name] = p
}

func (r *Registry) Get(name string) (ClaimProducer, bool) {
	p, ok := r.producers[name]
	return p, ok
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
	if err != nil || !ok {
		return nil, false
	}
	if _, exists := bindings[name]; !exists {
		return nil, false
	}
	return r.Get(proxyName)
}

func (r *Registry) Producers() map[string]ClaimProducer {
	out := make(map[string]ClaimProducer, len(r.producers))
	for name, p := range r.producers {
		out[name] = p
	}
	return out
}

func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.producers))
	for name := range r.producers {
		out = append(out, name)
	}
	return out
}

type closer interface {
	Close()
}

func (r *Registry) Close() {
	for _, p := range r.producers {
		if c, ok := p.(closer); ok {
			c.Close()
		}
	}
}
