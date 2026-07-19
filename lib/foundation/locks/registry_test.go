// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package locks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
)

type mockProducer struct {
	name       string
	closeCalls *int
}

func (m mockProducer) Name() string { return m.name }

func (m mockProducer) Capabilities(_ context.Context) (claimproducer.Capabilities, error) {
	return claimproducer.Capabilities{}, nil
}

func (m mockProducer) Open(_ context.Context, _ claimproducer.ClaimID, _ claimproducer.ClaimSpec) (claimproducer.OpenOutcome, error) {
	return claimproducer.OpenOutcome{}, nil
}

func (m mockProducer) Commit(_ context.Context, _ claimproducer.ClaimID, _, _ []byte) (claimproducer.CommitResult, error) {
	return claimproducer.CommitResult{}, nil
}

func (m mockProducer) Abandon(_ context.Context, _ claimproducer.ClaimID, _, _ []byte) error {
	return nil
}

func (m mockProducer) Release(_ context.Context, _ claimproducer.ClaimID, _, _ []byte) error {
	return nil
}

func (m mockProducer) SplitScope(_ context.Context, _ claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
	return claimproducer.SplitClaimScopeResponse{}, nil
}

func (m mockProducer) ScopesConflict(_ context.Context, _, _ []byte) (bool, error) {
	return false, nil
}

func (m mockProducer) Close() {
	if m.closeCalls != nil {
		*m.closeCalls++
	}
}

type bareMockProducer struct {
	name string
}

func (m bareMockProducer) Name() string { return m.name }

func (m bareMockProducer) Capabilities(_ context.Context) (claimproducer.Capabilities, error) {
	return claimproducer.Capabilities{}, nil
}

func (m bareMockProducer) Open(_ context.Context, _ claimproducer.ClaimID, _ claimproducer.ClaimSpec) (claimproducer.OpenOutcome, error) {
	return claimproducer.OpenOutcome{}, nil
}

func (m bareMockProducer) Commit(_ context.Context, _ claimproducer.ClaimID, _, _ []byte) (claimproducer.CommitResult, error) {
	return claimproducer.CommitResult{}, nil
}

func (m bareMockProducer) Abandon(_ context.Context, _ claimproducer.ClaimID, _, _ []byte) error {
	return nil
}

func (m bareMockProducer) Release(_ context.Context, _ claimproducer.ClaimID, _, _ []byte) error {
	return nil
}

func (m bareMockProducer) SplitScope(_ context.Context, _ claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
	return claimproducer.SplitClaimScopeResponse{}, nil
}

func (m bareMockProducer) ScopesConflict(_ context.Context, _, _ []byte) (bool, error) {
	return false, nil
}

func TestRegistryAddGet(t *testing.T) {
	r := NewRegistry()
	p := mockProducer{name: "alpha"}
	r.Add("alpha", p)

	got, ok := r.Get("alpha")
	if !ok {
		t.Fatalf("Get(%q) = false, want true", "alpha")
	}
	if got.Name() != "alpha" {
		t.Fatalf("Get(%q).Name() = %q, want %q", "alpha", got.Name(), "alpha")
	}

	if _, ok := r.Get("missing"); ok {
		t.Fatalf("Get(%q) = true, want false", "missing")
	}
}

func TestRegistryAddNilProducerDoesNotPanic(t *testing.T) {
	r := NewRegistry()
	r.Add("nil-producer", nil)

	got, ok := r.Get("nil-producer")
	if !ok {
		t.Fatalf("Get(%q) = false, want true", "nil-producer")
	}
	if got != nil {
		t.Fatalf("Get(%q) = %v, want nil", "nil-producer", got)
	}
}

func captureDefaultLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestRegistryAddNameDisagreementLogsWarning(t *testing.T) {
	buf := captureDefaultLog(t)

	r := NewRegistry()
	r.Add("registration-name", mockProducer{name: "internal-name"})

	logged := buf.String()
	if !strings.Contains(logged, "registration name disagrees with ClaimProducer.Name()") {
		t.Fatalf("expected name-disagreement warning, got: %q", logged)
	}
	if !strings.Contains(logged, "registration-name") || !strings.Contains(logged, "internal-name") {
		t.Fatalf("expected warning to name both the registration and internal names, got: %q", logged)
	}
}

func TestRegistryAddNameAgreementLogsNothing(t *testing.T) {
	buf := captureDefaultLog(t)

	r := NewRegistry()
	r.Add("alpha", mockProducer{name: "alpha"})

	if logged := buf.String(); logged != "" {
		t.Fatalf("expected no warning when names agree, got: %q", logged)
	}
}

func TestRegistryAddEmptyInternalNameLogsNothing(t *testing.T) {
	buf := captureDefaultLog(t)

	r := NewRegistry()
	r.Add("alpha", mockProducer{name: ""})

	if logged := buf.String(); logged != "" {
		t.Fatalf("expected no warning when internal name is empty, got: %q", logged)
	}
}

func TestRegistryGetWithContextDirectHit(t *testing.T) {
	r := NewRegistry()
	r.Add("alpha", mockProducer{name: "alpha"})

	got, ok := r.GetWithContext(context.Background(), "alpha", "instance-1")
	if !ok || got == nil {
		t.Fatalf("GetWithContext direct hit: got (%v, %v), want a producer and true", got, ok)
	}
}

func TestRegistryGetWithContextEmptyInstanceID(t *testing.T) {
	r := NewRegistry(
		WithLookupInstanceBindings(func(_ context.Context, _ string) (map[string]json.RawMessage, bool, error) {
			t.Fatalf("lookupInstanceBindings must not be called when instanceID is empty")
			return nil, false, nil
		}),
		WithLateBindServiceProxies(map[string]string{"claim_producer": "proxy"}),
	)

	got, ok := r.GetWithContext(context.Background(), "missing", "")
	if ok || got != nil {
		t.Fatalf("GetWithContext with empty instanceID = (%v, %v), want (nil, false)", got, ok)
	}
}

func TestRegistryGetWithContextNoLookupFn(t *testing.T) {
	r := NewRegistry(WithLateBindServiceProxies(map[string]string{"claim_producer": "proxy"}))

	got, ok := r.GetWithContext(context.Background(), "missing", "instance-1")
	if ok || got != nil {
		t.Fatalf("GetWithContext with no lookup fn = (%v, %v), want (nil, false)", got, ok)
	}
}

func TestRegistryGetWithContextNoProxyMapping(t *testing.T) {
	r := NewRegistry(
		WithLookupInstanceBindings(func(_ context.Context, _ string) (map[string]json.RawMessage, bool, error) {
			t.Fatalf("lookupInstanceBindings must not be called when no proxy mapping is configured")
			return nil, false, nil
		}),
	)

	got, ok := r.GetWithContext(context.Background(), "missing", "instance-1")
	if ok || got != nil {
		t.Fatalf("GetWithContext with no proxy mapping = (%v, %v), want (nil, false)", got, ok)
	}
}

func TestRegistryGetWithContextEmptyProxyName(t *testing.T) {
	r := NewRegistry(
		WithLookupInstanceBindings(func(_ context.Context, _ string) (map[string]json.RawMessage, bool, error) {
			t.Fatalf("lookupInstanceBindings must not be called when the proxy name is empty")
			return nil, false, nil
		}),
		WithLateBindServiceProxies(map[string]string{"claim_producer": ""}),
	)

	got, ok := r.GetWithContext(context.Background(), "missing", "instance-1")
	if ok || got != nil {
		t.Fatalf("GetWithContext with empty proxy name = (%v, %v), want (nil, false)", got, ok)
	}
}

func TestRegistryGetWithContextLookupError(t *testing.T) {
	r := NewRegistry(
		WithLookupInstanceBindings(func(_ context.Context, _ string) (map[string]json.RawMessage, bool, error) {
			return nil, false, errors.New("boom")
		}),
		WithLateBindServiceProxies(map[string]string{"claim_producer": "proxy"}),
	)
	r.Add("proxy", mockProducer{name: "proxy"})

	got, ok := r.GetWithContext(context.Background(), "missing", "instance-1")
	if ok || got != nil {
		t.Fatalf("GetWithContext with lookup error = (%v, %v), want (nil, false)", got, ok)
	}
}

func TestRegistryGetWithContextLookupNotFound(t *testing.T) {
	r := NewRegistry(
		WithLookupInstanceBindings(func(_ context.Context, _ string) (map[string]json.RawMessage, bool, error) {
			return nil, false, nil
		}),
		WithLateBindServiceProxies(map[string]string{"claim_producer": "proxy"}),
	)
	r.Add("proxy", mockProducer{name: "proxy"})

	got, ok := r.GetWithContext(context.Background(), "missing", "instance-1")
	if ok || got != nil {
		t.Fatalf("GetWithContext with lookup ok=false = (%v, %v), want (nil, false)", got, ok)
	}
}

func TestRegistryGetWithContextBindingAbsent(t *testing.T) {
	r := NewRegistry(
		WithLookupInstanceBindings(func(_ context.Context, _ string) (map[string]json.RawMessage, bool, error) {
			return map[string]json.RawMessage{"other-producer": json.RawMessage(`{}`)}, true, nil
		}),
		WithLateBindServiceProxies(map[string]string{"claim_producer": "proxy"}),
	)
	r.Add("proxy", mockProducer{name: "proxy"})

	got, ok := r.GetWithContext(context.Background(), "missing", "instance-1")
	if ok || got != nil {
		t.Fatalf("GetWithContext with binding absent = (%v, %v), want (nil, false)", got, ok)
	}
}

func TestRegistryGetWithContextProxyHit(t *testing.T) {
	r := NewRegistry(
		WithLookupInstanceBindings(func(_ context.Context, instanceID string) (map[string]json.RawMessage, bool, error) {
			if instanceID != "instance-1" {
				t.Fatalf("lookupInstanceBindings called with instanceID %q, want %q", instanceID, "instance-1")
			}
			return map[string]json.RawMessage{"missing": json.RawMessage(`{}`)}, true, nil
		}),
		WithLateBindServiceProxies(map[string]string{"claim_producer": "proxy"}),
	)
	r.Add("proxy", mockProducer{name: "proxy"})

	got, ok := r.GetWithContext(context.Background(), "missing", "instance-1")
	if !ok || got == nil {
		t.Fatalf("GetWithContext proxy hit = (%v, %v), want a producer and true", got, ok)
	}
	if got.Name() != "proxy" {
		t.Fatalf("GetWithContext proxy hit resolved producer %q, want %q", got.Name(), "proxy")
	}
}

func TestRegistryGetWithContextNilContextSubstituted(t *testing.T) {
	var sawContext bool
	r := NewRegistry(
		WithLookupInstanceBindings(func(ctx context.Context, _ string) (map[string]json.RawMessage, bool, error) {
			sawContext = ctx != nil
			return map[string]json.RawMessage{"missing": json.RawMessage(`{}`)}, true, nil
		}),
		WithLateBindServiceProxies(map[string]string{"claim_producer": "proxy"}),
	)
	r.Add("proxy", mockProducer{name: "proxy"})

	//nolint:staticcheck // exercising the registry's own nil-context substitution branch
	got, ok := r.GetWithContext(nil, "missing", "instance-1")
	if !ok || got == nil {
		t.Fatalf("GetWithContext with nil ctx = (%v, %v), want a producer and true", got, ok)
	}
	if !sawContext {
		t.Fatalf("GetWithContext did not substitute a non-nil context for the lookup function")
	}
}

func TestRegistryProducersAndNames(t *testing.T) {
	r := NewRegistry()
	r.Add("alpha", mockProducer{name: "alpha"})
	r.Add("beta", mockProducer{name: "beta"})

	names := r.Names()
	if len(names) != 2 {
		t.Fatalf("Names() = %v, want 2 entries", names)
	}
	byName := map[string]bool{}
	for _, n := range names {
		byName[n] = true
	}
	if !byName["alpha"] || !byName["beta"] {
		t.Fatalf("Names() = %v, want alpha and beta", names)
	}

	producers := r.Producers()
	if len(producers) != 2 {
		t.Fatalf("Producers() = %v, want 2 entries", producers)
	}
	producers["alpha"] = nil
	if got, _ := r.Get("alpha"); got == nil {
		t.Fatalf("mutating the map returned by Producers() must not affect the registry")
	}
}

func TestRegistryCloseDispatchesToImplementers(t *testing.T) {
	var closeCalls int
	r := NewRegistry()
	r.Add("closable", mockProducer{name: "closable", closeCalls: &closeCalls})
	r.Add("not-closable", bareMockProducer{name: "not-closable"})

	r.Close()

	if closeCalls != 1 {
		t.Fatalf("Close() dispatched %d times to the closer implementer, want 1", closeCalls)
	}
}

func TestNamedLocksConfigGet(t *testing.T) {
	cfg := NamedLocksConfig{Locks: map[string]NamedLockConfig{"alpha": {Limit: 3}}}

	got, ok := cfg.Get("alpha")
	if !ok || got.Limit != 3 {
		t.Fatalf("Get(%q) = (%v, %v), want (limit=3, true)", "alpha", got, ok)
	}

	if _, ok := cfg.Get("missing"); ok {
		t.Fatalf("Get(%q) = true, want false", "missing")
	}
}

func TestNamedLocksConfigGetNilMap(t *testing.T) {
	var cfg NamedLocksConfig

	if _, ok := cfg.Get("alpha"); ok {
		t.Fatalf("Get on a NamedLocksConfig with a nil map = true, want false")
	}
}

func TestNamedLocksConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     NamedLocksConfig
		wantErr bool
	}{
		{"empty", NamedLocksConfig{}, false},
		{"valid limit", NamedLocksConfig{Locks: map[string]NamedLockConfig{"alpha": {Limit: 1}}}, false},
		{"zero limit", NamedLocksConfig{Locks: map[string]NamedLockConfig{"alpha": {Limit: 0}}}, true},
		{"negative limit", NamedLocksConfig{Locks: map[string]NamedLockConfig{"alpha": {Limit: -1}}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
