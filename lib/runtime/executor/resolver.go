// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

type Endpoint struct {
	Transport string
	URL       string
	TLS       string
}

type DispatchContext struct {
	Ctx        context.Context
	InstanceID string
}

type Resolver interface {
	Resolve(name string, ctx DispatchContext) (Endpoint, bool)
	AcceptedNames() []string
}

type ErrorAwareResolver interface {
	ResolveWithError(name string, ctx DispatchContext) (Endpoint, bool, error)
}

func ResolveExecutor(r Resolver, name string, ctx DispatchContext) (Endpoint, bool, error) {
	if er, ok := r.(ErrorAwareResolver); ok {
		return er.ResolveWithError(name, ctx)
	}
	ep, ok := r.Resolve(name, ctx)
	return ep, ok, nil
}

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

// @concept: executor
func (r *StaticResolver) Register(name string, ep Endpoint) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[name] = ep
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

type LateBindResolver struct {
	static          Resolver
	lookupBindings  func(ctx context.Context, instanceID string) (map[string]json.RawMessage, bool, error)
	lateBindProxies map[string]string
}

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
	ep, ok, _ := r.ResolveWithError(name, ctx)
	return ep, ok
}

func (r *LateBindResolver) ResolveWithError(name string, ctx DispatchContext) (Endpoint, bool, error) {
	if ep, ok := r.static.Resolve(name, ctx); ok {
		return ep, true, nil
	}
	if ctx.InstanceID == "" {
		return Endpoint{}, false, nil
	}
	if r.lookupBindings == nil || len(r.lateBindProxies) == 0 {
		return Endpoint{}, false, nil
	}
	proxyName, ok := r.lateBindProxies["executor"]
	if !ok || proxyName == "" {
		return Endpoint{}, false, nil
	}
	lookupCtx := ctx.Ctx
	if lookupCtx == nil {
		lookupCtx = context.Background()
	}
	bindings, ok, err := r.lookupBindings(lookupCtx, ctx.InstanceID)
	if err != nil {
		return Endpoint{}, false, fmt.Errorf("LateBindResolver.ResolveWithError: lookupBindings(%s): %w", ctx.InstanceID, err)
	}
	if !ok {
		return Endpoint{}, false, nil
	}
	if _, exists := bindings[name]; !exists {
		return Endpoint{}, false, nil
	}
	ep, ok := r.static.Resolve(proxyName, ctx)
	return ep, ok, nil
}

func (r *LateBindResolver) AcceptedNames() []string {
	return r.static.AcceptedNames()
}

// @concept: executor
func (r *LateBindResolver) Unwrap() Resolver {
	return r.static
}
