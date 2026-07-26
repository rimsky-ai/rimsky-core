// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package locks

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
)

type closableMockProducer struct {
	mockProducer
	closed *int
}

func (m closableMockProducer) Close() error {
	*m.closed++
	return nil
}

type addressBookFixture struct {
	mu           sync.Mutex
	clock        time.Time
	endpoints    map[string]ProducerEndpoint
	lookupErr    error
	dialErr      error
	lookupCalls  int
	dialCalls    int
	closedByName map[string]*int
	dialGate     map[string]chan struct{}
	dialStarted  map[string]chan struct{}
}

func newAddressBookFixture() *addressBookFixture {
	return &addressBookFixture{
		clock:        time.Unix(1000, 0),
		endpoints:    map[string]ProducerEndpoint{},
		closedByName: map[string]*int{},
		dialGate:     map[string]chan struct{}{},
		dialStarted:  map[string]chan struct{}{},
	}
}

func (f *addressBookFixture) now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.clock
}

func (f *addressBookFixture) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clock = f.clock.Add(d)
}

func (f *addressBookFixture) lookup(_ context.Context, name string, _ persistence.Tx) (ProducerEndpoint, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookupCalls++
	if f.lookupErr != nil {
		return ProducerEndpoint{}, false, f.lookupErr
	}
	ep, ok := f.endpoints[name]
	return ep, ok, nil
}

func (f *addressBookFixture) dial(_ context.Context, name string, _ ProducerEndpoint) (ClaimProducer, error) {
	f.mu.Lock()
	f.dialCalls++
	dialErr := f.dialErr
	started := f.dialStarted[name]
	gate := f.dialGate[name]
	closed := f.closedByName[name]
	if closed == nil {
		closed = new(int)
		f.closedByName[name] = closed
	}
	f.mu.Unlock()
	if started != nil {
		close(started)
	}
	if gate != nil {
		<-gate
	}
	if dialErr != nil {
		return nil, dialErr
	}
	return closableMockProducer{mockProducer: mockProducer{name: name}, closed: closed}, nil
}

func (f *addressBookFixture) registry(ttl time.Duration) *Registry {
	return NewRegistry(WithAddressBookResolution(f.lookup, f.dial, ttl, f.now))
}

func (f *addressBookFixture) counts() (lookups, dials int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lookupCalls, f.dialCalls
}

func (f *addressBookFixture) closedCount(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c := f.closedByName[name]; c != nil {
		return *c
	}
	return 0
}

func mustResolve(t *testing.T, r *Registry, name string) ClaimProducer {
	t.Helper()
	p, ok, err := r.ResolveWithContext(context.Background(), name, "", nil)
	if err != nil || !ok || p == nil {
		t.Fatalf("ResolveWithContext(%q) = (%v, %v, %v), want a producer, true, nil", name, p, ok, err)
	}
	return p
}

func TestAddressBookResolveCachesWithinTTL(t *testing.T) {
	f := newAddressBookFixture()
	f.endpoints["alpha"] = ProducerEndpoint{Transport: "grpc", Endpoint: "alpha:1"}
	r := f.registry(10 * time.Second)

	first := mustResolve(t, r, "alpha")
	f.advance(5 * time.Second)
	second := mustResolve(t, r, "alpha")

	if first != second {
		t.Fatalf("fresh cache hit must return the same producer instance")
	}
	lookups, dials := f.counts()
	if lookups != 1 || dials != 1 {
		t.Fatalf("within-TTL resolve must not re-lookup or re-dial: lookups=%d dials=%d, want 1/1", lookups, dials)
	}
}

func TestAddressBookResolveRefreshesWithoutRedialOnSameEndpoint(t *testing.T) {
	f := newAddressBookFixture()
	f.endpoints["alpha"] = ProducerEndpoint{Transport: "grpc", Endpoint: "alpha:1"}
	r := f.registry(10 * time.Second)

	first := mustResolve(t, r, "alpha")
	f.advance(11 * time.Second)
	second := mustResolve(t, r, "alpha")

	if first != second {
		t.Fatalf("unchanged endpoint after TTL expiry must keep the existing producer connection")
	}
	lookups, dials := f.counts()
	if lookups != 2 || dials != 1 {
		t.Fatalf("expired entry with unchanged endpoint must re-lookup but not re-dial: lookups=%d dials=%d, want 2/1", lookups, dials)
	}

	f.advance(5 * time.Second)
	third := mustResolve(t, r, "alpha")
	if third != first {
		t.Fatalf("refresh must reset the TTL window on the cached producer")
	}
	lookups, _ = f.counts()
	if lookups != 2 {
		t.Fatalf("refreshed entry must serve from cache within the new TTL window: lookups=%d, want 2", lookups)
	}
}

func TestAddressBookResolveRedialsOnEndpointChange(t *testing.T) {
	f := newAddressBookFixture()
	f.endpoints["alpha"] = ProducerEndpoint{Transport: "grpc", Endpoint: "alpha:1"}
	r := f.registry(10 * time.Second)

	mustResolve(t, r, "alpha")
	f.endpoints["alpha"] = ProducerEndpoint{Transport: "grpc", Endpoint: "alpha:2"}
	f.advance(11 * time.Second)
	mustResolve(t, r, "alpha")

	_, dials := f.counts()
	if dials != 2 {
		t.Fatalf("endpoint change must trigger a redial: dials=%d, want 2", dials)
	}
	if f.closedCount("alpha") != 1 {
		t.Fatalf("the superseded connection must be closed on redial: closed=%d, want 1", f.closedCount("alpha"))
	}
}

func TestAddressBookResolveClosesOnRemoval(t *testing.T) {
	f := newAddressBookFixture()
	f.endpoints["alpha"] = ProducerEndpoint{Transport: "grpc", Endpoint: "alpha:1"}
	r := f.registry(10 * time.Second)

	mustResolve(t, r, "alpha")
	delete(f.endpoints, "alpha")
	f.advance(11 * time.Second)

	p, ok, err := r.ResolveWithContext(context.Background(), "alpha", "", nil)
	if err != nil || ok || p != nil {
		t.Fatalf("removal from the address book is an authoritative miss: got (%v, %v, %v), want (nil, false, nil)", p, ok, err)
	}
	if f.closedCount("alpha") != 1 {
		t.Fatalf("the removed producer's connection must be closed: closed=%d, want 1", f.closedCount("alpha"))
	}
}

func TestAddressBookResolveCloseClosesRemoteProducers(t *testing.T) {
	f := newAddressBookFixture()
	f.endpoints["alpha"] = ProducerEndpoint{Transport: "grpc", Endpoint: "alpha:1"}
	f.endpoints["beta"] = ProducerEndpoint{Transport: "grpc", Endpoint: "beta:1"}
	r := f.registry(10 * time.Second)

	mustResolve(t, r, "alpha")
	mustResolve(t, r, "beta")
	r.Close()

	if f.closedCount("alpha") != 1 || f.closedCount("beta") != 1 {
		t.Fatalf("Close must close every cached remote producer: alpha=%d beta=%d, want 1/1",
			f.closedCount("alpha"), f.closedCount("beta"))
	}
}

func TestAddressBookResolveServesStaleOnLookupError(t *testing.T) {
	f := newAddressBookFixture()
	f.endpoints["alpha"] = ProducerEndpoint{Transport: "grpc", Endpoint: "alpha:1"}
	r := f.registry(10 * time.Second)

	first := mustResolve(t, r, "alpha")
	f.mu.Lock()
	f.lookupErr = errors.New("address book down")
	f.mu.Unlock()
	f.advance(11 * time.Second)

	second := mustResolve(t, r, "alpha")
	if second != first {
		t.Fatalf("a transient lookup error with a cached connection must serve the stale producer")
	}
}

func TestAddressBookResolveLookupErrorWithoutCacheIsTransient(t *testing.T) {
	f := newAddressBookFixture()
	f.mu.Lock()
	f.lookupErr = errors.New("address book down")
	f.mu.Unlock()
	r := f.registry(10 * time.Second)

	p, ok, err := r.ResolveWithContext(context.Background(), "alpha", "", nil)
	if err == nil || ok || p != nil {
		t.Fatalf("lookup error with no cache must surface as an error, got (%v, %v, %v)", p, ok, err)
	}
	if !strings.Contains(err.Error(), "transient infra fault") {
		t.Fatalf("lookup error must be named transient, not an authoritative miss: %v", err)
	}
}

func TestAddressBookResolveDialErrorBehavior(t *testing.T) {
	f := newAddressBookFixture()
	f.endpoints["alpha"] = ProducerEndpoint{Transport: "grpc", Endpoint: "alpha:1"}
	f.mu.Lock()
	f.dialErr = errors.New("connection refused")
	f.mu.Unlock()
	r := f.registry(10 * time.Second)

	p, ok, err := r.ResolveWithContext(context.Background(), "alpha", "", nil)
	if err == nil || ok || p != nil {
		t.Fatalf("dial error with no cache must surface as an error, got (%v, %v, %v)", p, ok, err)
	}
	if !strings.Contains(err.Error(), "transient infra fault") {
		t.Fatalf("dial error must be named transient, not an authoritative miss: %v", err)
	}

	f.mu.Lock()
	f.dialErr = nil
	f.mu.Unlock()
	first := mustResolve(t, r, "alpha")

	f.endpoints["alpha"] = ProducerEndpoint{Transport: "grpc", Endpoint: "alpha:2"}
	f.mu.Lock()
	f.dialErr = errors.New("connection refused")
	f.mu.Unlock()
	f.advance(11 * time.Second)
	stale := mustResolve(t, r, "alpha")
	if stale != first {
		t.Fatalf("a dial error with a cached connection must serve the stale producer")
	}
}

func TestAddressBookResolveAfterCloseDialsAfresh(t *testing.T) {
	f := newAddressBookFixture()
	f.endpoints["alpha"] = ProducerEndpoint{Transport: "grpc", Endpoint: "alpha:1"}
	r := f.registry(10 * time.Second)
	mustResolve(t, r, "alpha")
	r.Close()

	mustResolve(t, r, "alpha")
	_, dials := f.counts()
	if dials != 2 {
		t.Fatalf("resolving after Close must dial afresh (the closed entry is discarded): dials=%d, want 2", dials)
	}
}

func TestAddressBookResolveCachedHitDoesNotBlockOnOtherNamesDial(t *testing.T) {
	f := newAddressBookFixture()
	f.endpoints["alpha"] = ProducerEndpoint{Transport: "grpc", Endpoint: "alpha:1"}
	f.endpoints["beta"] = ProducerEndpoint{Transport: "grpc", Endpoint: "beta:1"}
	f.dialGate["beta"] = make(chan struct{})
	f.dialStarted["beta"] = make(chan struct{})
	r := f.registry(10 * time.Second)

	mustResolve(t, r, "alpha")

	betaDone := make(chan struct{})
	go func() {
		defer close(betaDone)
		_, _, _ = r.ResolveWithContext(context.Background(), "beta", "", nil)
	}()
	<-f.dialStarted["beta"]

	mustResolve(t, r, "alpha")

	close(f.dialGate["beta"])
	<-betaDone
}
