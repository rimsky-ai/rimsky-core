// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package executor provides the supervisor-side executor client and
// name→endpoint resolver. Executors are peer services; rimsky never runs
// an executor in-process (except test stubs). Templates reference
// executors by name; the supervisor config maps names to endpoints.
package executor

import (
	"context"
	"encoding/json"
	"sync"
)

type Endpoint struct {
	Transport string // "grpc" | "http"
	URL       string
	TLS       string // "off" | "optional" | "required"
}

// DispatchContext carries instance/run-scope identity into resolver
// lookups. Named DispatchContext (rather than ResolveContext) to avoid
// the symbol clash with graph/attribute/substitution.go::ResolveContext,
// which is rimsky's existing substitution context.
type DispatchContext struct {
	// Ctx is the caller's context. Threaded into the late-bind binding
	// lookup (a DB hit) so it honors the dispatch's deadline/cancellation
	// rather than running uncancellable on context.Background(). May be
	// nil; resolvers fall back to context.Background() when so.
	Ctx        context.Context
	InstanceID string // empty for non-instance-scoped resolution
	RunScopeID string // ditto
}

type Resolver interface {
	Resolve(name string, ctx DispatchContext) (Endpoint, bool)
	// AcceptedNames returns all configured executor names. Used as the
	// supervisor's accept list when claiming dispatch rows.
	AcceptedNames() []string
}

// StaticResolver is backed by a map set at supervisor startup.
type StaticResolver struct {
	mu sync.RWMutex
	m  map[string]Endpoint
}

func NewStaticResolver(m map[string]Endpoint) *StaticResolver {
	cp := make(map[string]Endpoint, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return &StaticResolver{m: cp}
}

func (r *StaticResolver) Resolve(name string, _ DispatchContext) (Endpoint, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.m[name]
	return e, ok
}

func (r *StaticResolver) AcceptedNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.m))
	for n := range r.m {
		out = append(out, n)
	}
	return out
}

// LateBindResolver chains after a static resolver. For names not in
// the static map, it consults a per-instance service_bindings lookup
// hook and a static late_bind_service_proxies map (loaded from
// rimsky.yml). If both produce a hit, it returns the proxy's endpoint
// (resolved via the underlying static resolver).
//
// The dispatch path attaches the original service name to the call
// context via the per-call gRPC interceptor (Pass 4); the proxy
// reads the header to route. LateBindResolver does not add any
// metadata to the returned Endpoint.
type LateBindResolver struct {
	static          Resolver
	lookupBindings  func(ctx context.Context, instanceID string) (map[string]json.RawMessage, bool, error)
	lateBindProxies map[string]string // protocol → proxy service name
}

// NewLateBindResolver wraps a static resolver with late-bind fallback.
// When lookupBindings is nil or lateBindProxies is empty, the resolver
// behaves as a passthrough — the static resolver's results are
// returned unchanged.
func NewLateBindResolver(
	static Resolver,
	lookupBindings func(ctx context.Context, instanceID string) (map[string]json.RawMessage, bool, error),
	lateBindProxies map[string]string,
) *LateBindResolver {
	return &LateBindResolver{
		static:          static,
		lookupBindings:  lookupBindings,
		lateBindProxies: lateBindProxies,
	}
}

func (r *LateBindResolver) Resolve(name string, ctx DispatchContext) (Endpoint, bool) {
	if ep, ok := r.static.Resolve(name, ctx); ok {
		return ep, true
	}
	if ctx.InstanceID == "" {
		return Endpoint{}, false
	}
	if r.lookupBindings == nil || len(r.lateBindProxies) == 0 {
		return Endpoint{}, false
	}
	proxyName, ok := r.lateBindProxies["executor"]
	if !ok || proxyName == "" {
		return Endpoint{}, false
	}
	lookupCtx := ctx.Ctx
	if lookupCtx == nil {
		lookupCtx = context.Background()
	}
	bindings, ok, err := r.lookupBindings(lookupCtx, ctx.InstanceID)
	if err != nil || !ok {
		return Endpoint{}, false
	}
	if _, exists := bindings[name]; !exists {
		return Endpoint{}, false
	}
	return r.static.Resolve(proxyName, ctx)
}

func (r *LateBindResolver) AcceptedNames() []string {
	return r.static.AcceptedNames()
}
