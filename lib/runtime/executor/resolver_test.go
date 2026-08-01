// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package executor

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

type addressBookLookupFixture struct {
	mu      sync.Mutex
	clock   time.Time
	ep      Endpoint
	found   bool
	err     error
	lookups int
}

func newAddressBookLookupFixture() *addressBookLookupFixture {
	return &addressBookLookupFixture{clock: time.Unix(1000, 0)}
}

func (f *addressBookLookupFixture) now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.clock
}

func (f *addressBookLookupFixture) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clock = f.clock.Add(d)
}

func (f *addressBookLookupFixture) set(ep Endpoint, found bool, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ep, f.found, f.err = ep, found, err
}

func (f *addressBookLookupFixture) lookup(_ context.Context, _ string) (Endpoint, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups++
	if f.err != nil {
		return Endpoint{}, false, f.err
	}
	return f.ep, f.found, nil
}

func (f *addressBookLookupFixture) lookupCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lookups
}

func TestAddressBookResolver_TTLCache(t *testing.T) {
	epA := Endpoint{Transport: "grpc", URL: "exec-a:1"}
	epB := Endpoint{Transport: "grpc", URL: "exec-a:2"}
	cases := []struct {
		name        string
		advance     time.Duration
		wantEP      Endpoint
		wantLookups int
	}{
		{"within TTL serves cached endpoint", 5 * time.Second, epA, 1},
		{"after TTL re-looks-up and serves the fresh endpoint", 11 * time.Second, epB, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAddressBookLookupFixture()
			f.set(epA, true, nil)
			r := NewAddressBookResolver(nil, f.lookup, 10*time.Second, f.now)

			ep, ok, err := r.ResolveWithError("exec-a", DispatchContext{})
			if err != nil || !ok || ep != epA {
				t.Fatalf("first resolve = (%+v, %v, %v), want (%+v, true, nil)", ep, ok, err, epA)
			}
			f.set(epB, true, nil)
			f.advance(tc.advance)
			ep, ok, err = r.ResolveWithError("exec-a", DispatchContext{})
			if err != nil || !ok || ep != tc.wantEP {
				t.Fatalf("second resolve = (%+v, %v, %v), want (%+v, true, nil)", ep, ok, err, tc.wantEP)
			}
			if got := f.lookupCount(); got != tc.wantLookups {
				t.Fatalf("lookup count = %d, want %d", got, tc.wantLookups)
			}
		})
	}
}

func TestAddressBookResolver_NegativeCaching(t *testing.T) {
	f := newAddressBookLookupFixture()
	f.set(Endpoint{}, false, nil)
	r := NewAddressBookResolver(nil, f.lookup, 10*time.Second, f.now)

	if _, ok, err := r.ResolveWithError("missing", DispatchContext{}); ok || err != nil {
		t.Fatalf("authoritative miss must return (false, nil), got (%v, %v)", ok, err)
	}
	f.advance(5 * time.Second)
	if _, ok, err := r.ResolveWithError("missing", DispatchContext{}); ok || err != nil {
		t.Fatalf("cached miss must return (false, nil), got (%v, %v)", ok, err)
	}
	if got := f.lookupCount(); got != 1 {
		t.Fatalf("a miss within the TTL must be served from the negative cache: lookups=%d, want 1", got)
	}

	ep := Endpoint{Transport: "grpc", URL: "exec-a:1"}
	f.set(ep, true, nil)
	f.advance(11 * time.Second)
	got, ok, err := r.ResolveWithError("missing", DispatchContext{})
	if err != nil || !ok || got != ep {
		t.Fatalf("negative entry must expire with the TTL: got (%+v, %v, %v), want (%+v, true, nil)", got, ok, err, ep)
	}
}

func TestAddressBookResolver_StaleServeOnError(t *testing.T) {
	ep := Endpoint{Transport: "grpc", URL: "exec-a:1"}
	f := newAddressBookLookupFixture()
	f.set(ep, true, nil)
	r := NewAddressBookResolver(nil, f.lookup, 10*time.Second, f.now)

	if _, ok, err := r.ResolveWithError("exec-a", DispatchContext{}); !ok || err != nil {
		t.Fatalf("seed resolve failed: (%v, %v)", ok, err)
	}
	f.set(Endpoint{}, false, errors.New("address book down"))
	f.advance(11 * time.Second)
	got, ok, err := r.ResolveWithError("exec-a", DispatchContext{})
	if err != nil || !ok || got != ep {
		t.Fatalf("a transient lookup error with a cached entry must serve stale: got (%+v, %v, %v), want (%+v, true, nil)", got, ok, err, ep)
	}
}

func TestAddressBookResolver_ErrorWithoutCacheIsSurfaced(t *testing.T) {
	wantErr := errors.New("address book down")
	f := newAddressBookLookupFixture()
	f.set(Endpoint{}, false, wantErr)
	r := NewAddressBookResolver(nil, f.lookup, 10*time.Second, f.now)

	_, ok, err := r.ResolveWithError("exec-a", DispatchContext{})
	if ok || !errors.Is(err, wantErr) {
		t.Fatalf("lookup error with no cache must surface: got (%v, %v), want (false, wrapping %v)", ok, err, wantErr)
	}
}

func TestAddressBookResolver_StaticTakesPrecedence(t *testing.T) {
	staticEP := Endpoint{Transport: "grpc", URL: "static:1"}
	f := newAddressBookLookupFixture()
	f.set(Endpoint{Transport: "grpc", URL: "book:1"}, true, nil)
	r := NewAddressBookResolver(NewStaticResolver(map[string]Endpoint{"exec-a": staticEP}), f.lookup, 10*time.Second, f.now)

	ep, ok, err := r.ResolveWithError("exec-a", DispatchContext{})
	if err != nil || !ok || ep != staticEP {
		t.Fatalf("static registration must win over the address book: got (%+v, %v, %v), want (%+v, true, nil)", ep, ok, err, staticEP)
	}
	if got := f.lookupCount(); got != 0 {
		t.Fatalf("a static hit must not consult the address book: lookups=%d, want 0", got)
	}
}

func TestLateBindResolver_ResolveWithError_SurfacesLookupError(t *testing.T) {
	wantErr := errors.New("binding lookup: transient db error")
	r := NewLateBindResolver(
		NewStaticResolver(map[string]Endpoint{}),
		func(context.Context, string) (map[string]json.RawMessage, bool, error) {
			return nil, false, wantErr
		},
		map[string]string{"executor": "host-agent-proxy"},
	)

	ep, ok, err := r.ResolveWithError("late-bound-executor", DispatchContext{InstanceID: "inst-1"})
	if err == nil {
		t.Fatalf("ResolveWithError must surface the lookupBindings error, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("ResolveWithError error = %v, want wrapping %v", err, wantErr)
	}
	if ok {
		t.Fatalf("ResolveWithError ok = true on a lookup error, want false")
	}
	if ep != (Endpoint{}) {
		t.Fatalf("ResolveWithError endpoint = %+v, want zero value on error", ep)
	}
}

func TestLateBindResolver_Resolve_CompatSwallowsErrorToMiss(t *testing.T) {
	r := NewLateBindResolver(
		NewStaticResolver(map[string]Endpoint{}),
		func(context.Context, string) (map[string]json.RawMessage, bool, error) {
			return nil, false, errors.New("transient db error")
		},
		map[string]string{"executor": "host-agent-proxy"},
	)

	if _, ok := r.Resolve("late-bound-executor", DispatchContext{InstanceID: "inst-1"}); ok {
		t.Fatalf("Resolve ok = true on a lookup error, want false (2-value Resolve cannot distinguish miss from error)")
	}
}

func TestLateBindResolver_ResolveWithError_ProxiesOnSuccessfulBinding(t *testing.T) {
	proxyEP := Endpoint{Transport: "grpc", URL: "127.0.0.1:9"}
	static := NewStaticResolver(map[string]Endpoint{"host-agent-proxy": proxyEP})
	r := NewLateBindResolver(
		static,
		func(_ context.Context, instanceID string) (map[string]json.RawMessage, bool, error) {
			if instanceID != "inst-1" {
				return nil, false, nil
			}
			return map[string]json.RawMessage{"late-bound-executor": json.RawMessage(`{}`)}, true, nil
		},
		map[string]string{"executor": "host-agent-proxy"},
	)

	ep, ok, err := r.ResolveWithError("late-bound-executor", DispatchContext{InstanceID: "inst-1"})
	if err != nil {
		t.Fatalf("ResolveWithError: unexpected error: %v", err)
	}
	if !ok {
		t.Fatalf("ResolveWithError ok = false, want true (bound name should resolve to the proxy)")
	}
	if ep != proxyEP {
		t.Fatalf("ResolveWithError endpoint = %+v, want proxy endpoint %+v", ep, proxyEP)
	}
}

func TestResolveExecutor_PrefersErrorAwarePath(t *testing.T) {
	wantErr := errors.New("transient db error")
	r := NewLateBindResolver(
		NewStaticResolver(map[string]Endpoint{}),
		func(context.Context, string) (map[string]json.RawMessage, bool, error) {
			return nil, false, wantErr
		},
		map[string]string{"executor": "host-agent-proxy"},
	)

	_, ok, err := ResolveExecutor(r, "late-bound-executor", DispatchContext{InstanceID: "inst-1"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("ResolveExecutor error = %v, want wrapping %v", err, wantErr)
	}
	if ok {
		t.Fatalf("ResolveExecutor ok = true on a lookup error, want false")
	}
}

func TestResolveExecutor_FallsBackToPlainResolveWhenNotErrorAware(t *testing.T) {
	ep := Endpoint{Transport: "grpc", URL: "127.0.0.1:9"}
	static := NewStaticResolver(map[string]Endpoint{"exec-a": ep})

	got, ok, err := ResolveExecutor(static, "exec-a", DispatchContext{})
	if err != nil {
		t.Fatalf("ResolveExecutor: unexpected error for a non-error-aware Resolver: %v", err)
	}
	if !ok || got != ep {
		t.Fatalf("ResolveExecutor(static) = (%+v, %v), want (%+v, true)", got, ok, ep)
	}
}
