// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package locks

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/internal/namedreg"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

type Registry struct {
	reg                    namedreg.Registry[ClaimProducer]
	lookupInstanceBindings func(ctx context.Context, instanceID string, tx persistence.Tx) (map[string]json.RawMessage, bool, error)
	lateBindServiceProxies map[string]string

	lookupEndpoint LookupProducerEndpoint
	dialProducer   DialProducer
	resolveTTL     time.Duration
	now            func() time.Time
	remoteMu       sync.Mutex
	remote         map[string]*remoteProducerEntry
}

// @concept: service-address-book
type ProducerEndpoint struct {
	Transport string
	Endpoint  string
	TLS       string
}

type LookupProducerEndpoint func(ctx context.Context, name string, tx persistence.Tx) (ProducerEndpoint, bool, error)

type DialProducer func(ctx context.Context, name string, ep ProducerEndpoint) (ClaimProducer, error)

type remoteProducerEntry struct {
	mu        sync.Mutex
	producer  ClaimProducer
	endpoint  ProducerEndpoint
	fetchedAt time.Time
	closed    bool
}

const DefaultAddressBookCacheTTL = 2 * time.Second

type Option func(*Registry)

func WithLookupInstanceBindings(fn func(ctx context.Context, instanceID string, tx persistence.Tx) (map[string]json.RawMessage, bool, error)) Option {
	return func(r *Registry) { r.lookupInstanceBindings = fn }
}

func WithLateBindServiceProxies(m map[string]string) Option {
	return func(r *Registry) { r.lateBindServiceProxies = m }
}

// @concept: service-address-book
func WithAddressBookResolution(lookup LookupProducerEndpoint, dial DialProducer, ttl time.Duration, now func() time.Time) Option {
	return func(r *Registry) {
		r.lookupEndpoint = lookup
		r.dialProducer = dial
		r.resolveTTL = ttl
		if r.resolveTTL <= 0 {
			r.resolveTTL = DefaultAddressBookCacheTTL
		}
		r.now = now
		if r.now == nil {
			r.now = time.Now
		}
	}
}

func NewRegistry(opts ...Option) *Registry {
	r := &Registry{reg: namedreg.New[ClaimProducer](), remote: map[string]*remoteProducerEntry{}}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func (r *Registry) Add(name string, p ClaimProducer) {
	if p != nil {
		if got := p.Name(); got != "" && got != name {
			slog.Warn("CLAIMPRODUCERREGISTRY.REGISTRATIONNAME.MISMATCHED", "detail", "the registration name disagrees with the name the producer reports",
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

// @concept: service-address-book
func (r *Registry) ResolveWithContext(ctx context.Context, name string, instanceID string, tx persistence.Tx) (ClaimProducer, bool, error) {
	if p, ok := r.Get(name); ok {
		return p, true, nil
	}
	if p, ok, err := r.resolveViaAddressBook(ctx, name, tx); err != nil {
		return nil, false, err
	} else if ok {
		return p, true, nil
	}
	return r.getLateBound(ctx, name, instanceID, tx)
}

// @concept: service-address-book
// @story: host-daemon-late-bind-all-protocols
func (r *Registry) getLateBound(ctx context.Context, name string, instanceID string, tx persistence.Tx) (ClaimProducer, bool, error) {
	if instanceID == "" {
		return nil, false, nil
	}
	if r.lookupInstanceBindings == nil {
		return nil, false, nil
	}
	proxyName, ok := r.lateBindServiceProxies["claim_producer"]
	if !ok || proxyName == "" {
		return nil, false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	bindings, ok, err := r.lookupInstanceBindings(ctx, instanceID, tx)
	if err != nil {
		slog.Warn("CLAIMPRODUCERREGISTRY.INSTANCEBINDINGS.LOOKUPFAILED", "detail", "classifying the row as an unknown claim producer",
			"instance_id", instanceID,
			"producer_name", name,
			"error", err.Error())
		return nil, false, nil
	}
	if !ok {
		return nil, false, nil
	}
	if _, exists := bindings[name]; !exists {
		return nil, false, nil
	}
	if p, ok := r.Get(proxyName); ok {
		return p, true, nil
	}
	return r.resolveViaAddressBook(ctx, proxyName, tx)
}

// @concept: service-address-book
func (r *Registry) resolveViaAddressBook(ctx context.Context, name string, tx persistence.Tx) (ClaimProducer, bool, error) {
	if r.lookupEndpoint == nil || r.dialProducer == nil {
		return nil, false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	r.remoteMu.Lock()
	entry := r.remote[name]
	if entry == nil {
		entry = &remoteProducerEntry{}
		r.remote[name] = entry
	}
	r.remoteMu.Unlock()
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.closed {
		return nil, false, fmt.Errorf("producer registry: closed while resolving %q", name)
	}
	now := r.now()
	if entry.producer != nil && now.Sub(entry.fetchedAt) < r.resolveTTL {
		return entry.producer, true, nil
	}
	ep, found, err := r.lookupEndpoint(ctx, name, tx)
	if err != nil {
		if entry.producer != nil {
			return entry.producer, true, nil
		}
		return nil, false, fmt.Errorf("producer registry: service-address-book lookup for %q failed (transient infra fault, not an authoritative miss): %w", name, err)
	}
	if !found {
		if entry.producer != nil {
			closeProducer(entry.producer)
			entry.producer = nil
		}
		return nil, false, nil
	}
	if entry.producer != nil && entry.endpoint == ep {
		entry.fetchedAt = now
		return entry.producer, true, nil
	}
	producer, err := r.dialProducer(ctx, name, ep)
	if err != nil {
		if entry.producer != nil {
			return entry.producer, true, nil
		}
		return nil, false, fmt.Errorf("producer registry: service-address-book dial for %q at %s failed (transient infra fault, not an authoritative miss): %w", name, ep.Endpoint, err)
	}
	if entry.producer != nil {
		closeProducer(entry.producer)
	}
	entry.producer, entry.endpoint, entry.fetchedAt = producer, ep, now
	return producer, true, nil
}

func closeProducer(p ClaimProducer) {
	if c, ok := p.(interface{ Close() error }); ok {
		_ = c.Close()
	}
}

func (r *Registry) Producers() map[string]ClaimProducer {
	return r.reg.CopyMap()
}

func (r *Registry) Names() []string {
	return r.reg.Names()
}

func (r *Registry) Close() {
	r.remoteMu.Lock()
	entries := make([]*remoteProducerEntry, 0, len(r.remote))
	for name, entry := range r.remote {
		entries = append(entries, entry)
		delete(r.remote, name)
	}
	r.remoteMu.Unlock()
	for _, entry := range entries {
		entry.mu.Lock()
		if entry.producer != nil {
			closeProducer(entry.producer)
			entry.producer = nil
		}
		entry.closed = true
		entry.mu.Unlock()
	}
	r.reg.CloseAll()
}
