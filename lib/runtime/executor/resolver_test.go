// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package executor

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

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
