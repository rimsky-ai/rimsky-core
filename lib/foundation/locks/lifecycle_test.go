// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package locks

import (
	"context"
	"strconv"
	"sync"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/lifecycle"
)

type mockSubscriber struct {
	name       string
	closeCalls *int
}

func (m mockSubscriber) Name() string { return m.name }

func (m mockSubscriber) OnTemplateRegistered(_ context.Context, _ lifecycle.OnTemplateRegisteredRequest) error {
	return nil
}

func (m mockSubscriber) OnTemplateDeployed(_ context.Context, _ lifecycle.OnTemplateDeployedRequest) error {
	return nil
}

func (m mockSubscriber) OnTemplateUndeployed(_ context.Context, _ lifecycle.OnTemplateUndeployedRequest) error {
	return nil
}

func (m mockSubscriber) OnTemplateDeregistered(_ context.Context, _ lifecycle.OnTemplateDeregisteredRequest) error {
	return nil
}

func (m mockSubscriber) OnInstanceCreated(_ context.Context, _ lifecycle.OnInstanceCreatedRequest) error {
	return nil
}

func (m mockSubscriber) OnInstanceTerminated(_ context.Context, _ lifecycle.OnInstanceTerminatedRequest) error {
	return nil
}

func (m mockSubscriber) OnRunScopeTerminal(_ context.Context, _ lifecycle.OnRunScopeTerminalRequest) error {
	return nil
}

func (m mockSubscriber) Close() {
	if m.closeCalls != nil {
		*m.closeCalls++
	}
}

type bareMockSubscriber struct {
	name string
}

func (m bareMockSubscriber) Name() string { return m.name }

func (m bareMockSubscriber) OnTemplateRegistered(_ context.Context, _ lifecycle.OnTemplateRegisteredRequest) error {
	return nil
}

func (m bareMockSubscriber) OnTemplateDeployed(_ context.Context, _ lifecycle.OnTemplateDeployedRequest) error {
	return nil
}

func (m bareMockSubscriber) OnTemplateUndeployed(_ context.Context, _ lifecycle.OnTemplateUndeployedRequest) error {
	return nil
}

func (m bareMockSubscriber) OnTemplateDeregistered(_ context.Context, _ lifecycle.OnTemplateDeregisteredRequest) error {
	return nil
}

func (m bareMockSubscriber) OnInstanceCreated(_ context.Context, _ lifecycle.OnInstanceCreatedRequest) error {
	return nil
}

func (m bareMockSubscriber) OnInstanceTerminated(_ context.Context, _ lifecycle.OnInstanceTerminatedRequest) error {
	return nil
}

func (m bareMockSubscriber) OnRunScopeTerminal(_ context.Context, _ lifecycle.OnRunScopeTerminalRequest) error {
	return nil
}

func TestLifecycleRegistryAddGet(t *testing.T) {
	r := NewLifecycleRegistry()
	r.Add("alpha", mockSubscriber{name: "alpha"})

	got, ok := r.Get("alpha")
	if !ok || got.Name() != "alpha" {
		t.Fatalf("Get(%q) = (%v, %v), want (alpha, true)", "alpha", got, ok)
	}

	if _, ok := r.Get("missing"); ok {
		t.Fatalf("Get(%q) = true, want false", "missing")
	}
}

func TestLifecycleRegistryNames(t *testing.T) {
	r := NewLifecycleRegistry()
	r.Add("alpha", mockSubscriber{name: "alpha"})
	r.Add("beta", mockSubscriber{name: "beta"})

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
}

func TestLifecycleRegistrySubscribers(t *testing.T) {
	r := NewLifecycleRegistry()
	r.Add("alpha", mockSubscriber{name: "alpha"})

	subs := r.Subscribers()
	if len(subs) != 1 {
		t.Fatalf("Subscribers() = %v, want 1 entry", subs)
	}
	subs["alpha"] = nil
	if got, _ := r.Get("alpha"); got == nil {
		t.Fatalf("mutating the map returned by Subscribers() must not affect the registry")
	}
}

func TestLifecycleRegistryConcurrentAddAndReadIsRaceFree(t *testing.T) {
	r := NewLifecycleRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := "sub-" + strconv.Itoa(i)
			r.Add(name, mockSubscriber{name: name})
		}(i)
	}
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Get("sub-0")
			r.Names()
			r.Subscribers()
		}()
	}
	wg.Wait()
}

func TestLifecycleRegistryCloseDispatchesToImplementers(t *testing.T) {
	var closeCalls int
	r := NewLifecycleRegistry()
	r.Add("closable", mockSubscriber{name: "closable", closeCalls: &closeCalls})
	r.Add("not-closable", bareMockSubscriber{name: "not-closable"})

	r.Close()

	if closeCalls != 1 {
		t.Fatalf("Close() dispatched %d times to the closer implementer, want 1", closeCalls)
	}
}
