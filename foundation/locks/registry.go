// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Registry — the per-process map from operator-chosen producer name to
// a ClaimProducer implementation. Per spec
// docs/specs/2026-05-04-service-protocol-contract.md §2.
//
// The Registry is a simple name→ClaimProducer map populated externally:
// each rimsky process's cmd binary loads rimsky.yml, dials a remote
// gRPC client per entry (runtime/peer/), validates
// the Capabilities() handshake (operator envelope ⊆ producer envelope),
// and Add()s the result. There is no Factory interface, no per-kind
// dispatch.
//
// NamedLocksConfig and named-lock helpers stay here unchanged.

package locks

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
)

// NamedLockConfig is the operator-side configuration for one named
// lock. Limit must be ≥ 1; limit=1 is a mutex, limit=N>1 is a counting
// semaphore. Templates reference named locks by name only; the limit
// lives here.
type NamedLockConfig struct {
	Limit int `yaml:"limit"`
}

// NamedLocksConfig is the top-level "named_locks" map from process
// YAML. Keys are operator-chosen lock names; each value carries the
// limit. Per spec §6.1 — stores and named locks live in one operator
// config bundle.
type NamedLocksConfig struct {
	Locks map[string]NamedLockConfig
}

// Get returns the configured NamedLockConfig for `name`, plus a
// presence bool. Used by the queue eligibility predicate and by
// template-deploy validation.
func (c NamedLocksConfig) Get(name string) (NamedLockConfig, bool) {
	if c.Locks == nil {
		return NamedLockConfig{}, false
	}
	cfg, ok := c.Locks[name]
	return cfg, ok
}

// Validate checks that every NamedLockConfig.Limit ≥ 1. Returns an
// error listing the offending names. Run at process startup; reject
// invalid configs loudly.
func (c NamedLocksConfig) Validate() error {
	for name, cfg := range c.Locks {
		if cfg.Limit < 1 {
			return fmt.Errorf("named_locks[%q]: limit must be >= 1, got %d", name, cfg.Limit)
		}
	}
	return nil
}

// Registry holds the per-process name→ClaimProducer map. Populated
// externally by each rimsky cmd binary's startup wiring; consumed by
// the supervisor's acquisition flow, the scheduler's orphan reaper,
// and the control-api's template validator.
type Registry struct {
	producers              map[string]ClaimProducer
	lookupInstanceBindings func(ctx context.Context, instanceID string) (map[string]json.RawMessage, bool, error)
	lateBindServiceProxies map[string]string
}

// Option configures a Registry at construction time.
type Option func(*Registry)

// WithLookupInstanceBindings supplies the persistence hook for
// late-bound claim-producer resolution. When nil (the default),
// the registry has no late-bind support.
func WithLookupInstanceBindings(fn func(ctx context.Context, instanceID string) (map[string]json.RawMessage, bool, error)) Option {
	return func(r *Registry) { r.lookupInstanceBindings = fn }
}

// WithLateBindServiceProxies supplies the per-protocol proxy-name
// map (loaded from rimsky.yml's late_bind_service_proxies). When
// empty or nil, the registry has no late-bind support.
func WithLateBindServiceProxies(m map[string]string) Option {
	return func(r *Registry) { r.lateBindServiceProxies = m }
}

// NewRegistry returns an empty Registry. Callers Add(name, producer)
// after dialing each remote producer-service. Optional functional
// options configure late-bind claim-producer resolution.
func NewRegistry(opts ...Option) *Registry {
	r := &Registry{producers: make(map[string]ClaimProducer)}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Add registers a ClaimProducer under name. Re-adding the same name
// overwrites the previous entry; the caller is responsible for avoiding
// accidental shadowing.
//
// As a defensive sanity check (logged at startup): if the producer
// reports a non-empty Name() that disagrees with the registration name,
// Add emits a slog.Warn but proceeds with the registration.
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

// Get returns the ClaimProducer registered under name and a boolean
// indicating presence.
func (r *Registry) Get(name string) (ClaimProducer, bool) {
	p, ok := r.producers[name]
	return p, ok
}

// GetWithContext is the late-bind-aware sibling of Get. When the
// registry was constructed without late-bind options (or no instance
// context is supplied), it falls through to Get(name) — behavior is
// identical to today. The caller's ctx is threaded into the binding
// lookup (a DB hit) so it honors the dispatch's deadline/cancellation
// rather than running uncancellable on context.Background(). A nil ctx
// falls back to context.Background().
//
// @diverged: true
// @reason: parallels runtime/executor/resolver.go::Resolver.Resolve(name, DispatchContext)
//
//	but uses a plain ctx + instanceID arg instead of a DispatchContext type
//	to avoid foundation→runtime imports (banned by layer-purity).
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

// Producers returns a snapshot of every registered ClaimProducer keyed
// by name. The returned map is a fresh copy; callers may mutate it
// without affecting the Registry.
func (r *Registry) Producers() map[string]ClaimProducer {
	out := make(map[string]ClaimProducer, len(r.producers))
	for name, p := range r.producers {
		out[name] = p
	}
	return out
}

// Names returns the set of registered producer names. Used to derive
// the supervisor's accepted_stores list for the dispatch eligibility
// predicate.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.producers))
	for name := range r.producers {
		out = append(out, name)
	}
	return out
}

// closer is the optional interface a producer implements when it owns
// disposable resources (e.g. a gRPC client connection). Registry.Close
// calls it on each registered producer; producers that don't implement
// it are no-ops.
type closer interface {
	Close()
}

// Close walks every registered producer and calls Close() on those that
// implement the closer interface. Intended for cmd binaries to call at
// shutdown so per-producer resources are released. Idempotent.
func (r *Registry) Close() {
	for _, p := range r.producers {
		if c, ok := p.(closer); ok {
			c.Close()
		}
	}
}
