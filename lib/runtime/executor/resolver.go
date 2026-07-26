// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
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

// @concept: service-address-book
type AddressBookLookup func(ctx context.Context, name string) (Endpoint, bool, error)

const DefaultAddressBookCacheTTL = 2 * time.Second

// @concept: service-address-book
type AddressBookResolver struct {
	static Resolver
	lookup AddressBookLookup
	ttl    time.Duration
	now    func() time.Time

	mu    sync.Mutex
	cache map[string]addressBookCacheEntry
}

type addressBookCacheEntry struct {
	ep        Endpoint
	found     bool
	fetchedAt time.Time
}

func NewAddressBookResolver(static Resolver, lookup AddressBookLookup, ttl time.Duration, now func() time.Time) *AddressBookResolver {
	if ttl <= 0 {
		ttl = DefaultAddressBookCacheTTL
	}
	if now == nil {
		now = time.Now
	}
	return &AddressBookResolver{
		static: static,
		lookup: lookup,
		ttl:    ttl,
		now:    now,
		cache:  map[string]addressBookCacheEntry{},
	}
}

func (r *AddressBookResolver) Resolve(name string, ctx DispatchContext) (Endpoint, bool) {
	ep, ok, _ := r.ResolveWithError(name, ctx)
	return ep, ok
}

func (r *AddressBookResolver) ResolveWithError(name string, ctx DispatchContext) (Endpoint, bool, error) {
	if r.static != nil {
		if ep, ok := r.static.Resolve(name, ctx); ok {
			return ep, true, nil
		}
	}
	now := r.now()
	r.mu.Lock()
	entry, cached := r.cache[name]
	r.mu.Unlock()
	if cached && now.Sub(entry.fetchedAt) < r.ttl {
		return entry.ep, entry.found, nil
	}
	lookupCtx := ctx.Ctx
	if lookupCtx == nil {
		lookupCtx = context.Background()
	}
	ep, found, err := r.lookup(lookupCtx, name)
	if err != nil {
		if cached {
			return entry.ep, entry.found, nil
		}
		return Endpoint{}, false, fmt.Errorf("AddressBookResolver.ResolveWithError: lookup %q: %w", name, err)
	}
	r.mu.Lock()
	r.cache[name] = addressBookCacheEntry{ep: ep, found: found, fetchedAt: now}
	r.mu.Unlock()
	return ep, found, nil
}

// @concept: executor
func (r *AddressBookResolver) Unwrap() Resolver {
	return r.static
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
	if ep, ok, err := ResolveExecutor(r.static, name, ctx); err != nil {
		return Endpoint{}, false, err
	} else if ok {
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
	ep, ok, err := ResolveExecutor(r.static, proxyName, ctx)
	if err != nil {
		return Endpoint{}, false, err
	}
	return ep, ok, nil
}

// @concept: executor
func (r *LateBindResolver) Unwrap() Resolver {
	return r.static
}
