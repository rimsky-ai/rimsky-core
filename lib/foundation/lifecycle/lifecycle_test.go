// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package lifecycle

import (
	"context"
	"strconv"
	"sync"
	"testing"

	protolifecycle "github.com/rimsky-ai/rimsky-core/lib/protocols/lifecycle"
)

type mockSubscriber struct {
	name       string
	closeCalls *int
}

func (m mockSubscriber) Name() string { return m.name }

func (m mockSubscriber) OnTemplateRegistered(_ context.Context, _ protolifecycle.OnTemplateRegisteredRequest) error {
	return nil
}

func (m mockSubscriber) OnTemplateDeployed(_ context.Context, _ protolifecycle.OnTemplateDeployedRequest) error {
	return nil
}

func (m mockSubscriber) OnTemplateUndeployed(_ context.Context, _ protolifecycle.OnTemplateUndeployedRequest) error {
	return nil
}

func (m mockSubscriber) OnTemplateDeregistered(_ context.Context, _ protolifecycle.OnTemplateDeregisteredRequest) error {
	return nil
}

func (m mockSubscriber) OnInstanceCreated(_ context.Context, _ protolifecycle.OnInstanceCreatedRequest) error {
	return nil
}

func (m mockSubscriber) OnInstanceTerminated(_ context.Context, _ protolifecycle.OnInstanceTerminatedRequest) error {
	return nil
}

func (m mockSubscriber) OnRunScopeTerminal(_ context.Context, _ protolifecycle.OnRunScopeTerminalRequest) error {
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

func (m bareMockSubscriber) OnTemplateRegistered(_ context.Context, _ protolifecycle.OnTemplateRegisteredRequest) error {
	return nil
}

func (m bareMockSubscriber) OnTemplateDeployed(_ context.Context, _ protolifecycle.OnTemplateDeployedRequest) error {
	return nil
}

func (m bareMockSubscriber) OnTemplateUndeployed(_ context.Context, _ protolifecycle.OnTemplateUndeployedRequest) error {
	return nil
}

func (m bareMockSubscriber) OnTemplateDeregistered(_ context.Context, _ protolifecycle.OnTemplateDeregisteredRequest) error {
	return nil
}

func (m bareMockSubscriber) OnInstanceCreated(_ context.Context, _ protolifecycle.OnInstanceCreatedRequest) error {
	return nil
}

func (m bareMockSubscriber) OnInstanceTerminated(_ context.Context, _ protolifecycle.OnInstanceTerminatedRequest) error {
	return nil
}

func (m bareMockSubscriber) OnRunScopeTerminal(_ context.Context, _ protolifecycle.OnRunScopeTerminalRequest) error {
	return nil
}

func TestRegistryAddGet(t *testing.T) {
	r := NewRegistry()
	r.Add("alpha", mockSubscriber{name: "alpha"})

	got, ok := r.Get("alpha")
	if !ok || got.Name() != "alpha" {
		t.Fatalf("Get(%q) = (%v, %v), want (alpha, true)", "alpha", got, ok)
	}

	if _, ok := r.Get("missing"); ok {
		t.Fatalf("Get(%q) = true, want false", "missing")
	}
}

func TestRegistryNames(t *testing.T) {
	r := NewRegistry()
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

func TestRegistrySubscribers(t *testing.T) {
	r := NewRegistry()
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

func TestRegistryConcurrentAddsAllLandAndEveryNameResolves(t *testing.T) {
	const subscribers = 20
	r := NewRegistry()
	var wg sync.WaitGroup
	snapshots := make([][]string, subscribers)
	for i := 0; i < subscribers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := "sub-" + strconv.Itoa(i)
			r.Add(name, mockSubscriber{name: name})
		}(i)
	}
	for i := 0; i < subscribers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			snapshots[i] = r.Names()
		}(i)
	}
	wg.Wait()

	for i := 0; i < subscribers; i++ {
		name := "sub-" + strconv.Itoa(i)
		sub, ok := r.Get(name)
		if !ok {
			t.Fatalf("Get(%q) after %d concurrent Add calls found nothing; an add was lost", name, subscribers)
		}
		if sub.Name() != name {
			t.Fatalf("Get(%q) returned the subscriber named %q", name, sub.Name())
		}
	}
	if got := len(r.Subscribers()); got != subscribers {
		t.Fatalf("Subscribers() holds %d entries after %d concurrent Add calls, want %d", got, subscribers, subscribers)
	}
	for i, snapshot := range snapshots {
		for _, name := range snapshot {
			if _, ok := r.Get(name); !ok {
				t.Fatalf("the concurrent Names() snapshot %d named %q, which Get does not resolve", i, name)
			}
		}
	}
}

func TestRegistryCloseDispatchesToImplementers(t *testing.T) {
	var closeCalls int
	r := NewRegistry()
	r.Add("closable", mockSubscriber{name: "closable", closeCalls: &closeCalls})
	r.Add("not-closable", bareMockSubscriber{name: "not-closable"})

	r.Close()

	if closeCalls != 1 {
		t.Fatalf("Close() dispatched %d times to the closer implementer, want 1", closeCalls)
	}
}
