// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package config

import (
	"context"
	"testing"

	"github.com/rimsky-ai/rimsky-core/lib/control/controlapi"
	"github.com/rimsky-ai/rimsky-core/lib/control/observability"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/locks"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
)

type bundledMergeMockProducer struct {
	name string
}

func (m bundledMergeMockProducer) Name() string { return m.name }

func (m bundledMergeMockProducer) Capabilities(context.Context) (claimproducer.Capabilities, error) {
	return claimproducer.Capabilities{}, nil
}

func (m bundledMergeMockProducer) Open(context.Context, claimproducer.ClaimID, claimproducer.ClaimSpec) (claimproducer.OpenOutcome, error) {
	return claimproducer.OpenOutcome{}, nil
}

func (m bundledMergeMockProducer) Commit(context.Context, claimproducer.ClaimID, []byte, []byte, string) (claimproducer.CommitResult, error) {
	return claimproducer.CommitResult{}, nil
}

func (m bundledMergeMockProducer) Abandon(context.Context, claimproducer.ClaimID, []byte, []byte, string) error {
	return nil
}

func (m bundledMergeMockProducer) Release(context.Context, claimproducer.ClaimID, []byte, []byte, string) error {
	return nil
}

func (m bundledMergeMockProducer) SplitScope(context.Context, claimproducer.SplitClaimScopeRequest) (claimproducer.SplitClaimScopeResponse, error) {
	return claimproducer.SplitClaimScopeResponse{}, nil
}

func (m bundledMergeMockProducer) ScopesConflict(context.Context, []byte, []byte) (bool, error) {
	return false, nil
}

func TestMergeBundledClaimProducers_ConfiguredWinsOverBundled(t *testing.T) {
	registry := locks.NewRegistry()
	configuredProducer := bundledMergeMockProducer{name: "configured-a"}
	registry.Add("store-a", configuredProducer)

	bundled := map[string]locks.ClaimProducer{
		"store-a": bundledMergeMockProducer{name: "bundled-a"},
		"store-b": bundledMergeMockProducer{name: "bundled-b"},
	}

	var overridden []string
	mergeBundledClaimProducers(registry, bundled, func(name string) {
		overridden = append(overridden, name)
	})

	got, ok := registry.Get("store-a")
	if !ok {
		t.Fatalf("store-a missing from registry after merge")
	}
	if gotProducer, ok := got.(bundledMergeMockProducer); !ok || gotProducer.name != "configured-a" {
		t.Fatalf("store-a was overwritten by bundled producer: got %+v, want the pre-registered configured producer", got)
	}

	got, ok = registry.Get("store-b")
	if !ok {
		t.Fatalf("store-b was not added from bundled registrations")
	}
	if gotProducer, ok := got.(bundledMergeMockProducer); !ok || gotProducer.name != "bundled-b" {
		t.Fatalf("store-b: got %+v, want bundled-b", got)
	}

	if len(overridden) != 1 || overridden[0] != "store-a" {
		t.Fatalf("onOverride callback: got %v, want exactly [store-a]", overridden)
	}
}

func TestMergeBundledClaimProducers_NoOverrideCallbackWhenNilProducerAdded(t *testing.T) {
	registry := locks.NewRegistry()
	bundled := map[string]locks.ClaimProducer{
		"store-only-bundled": bundledMergeMockProducer{name: "bundled-only"},
	}

	called := false
	mergeBundledClaimProducers(registry, bundled, func(string) { called = true })

	if called {
		t.Fatalf("onOverride callback fired for a name with no configured producer")
	}
	if _, ok := registry.Get("store-only-bundled"); !ok {
		t.Fatalf("bundled-only producer with no configured collision was not added")
	}
}

func TestMergeBundledExecutorEntries_ConfiguredWinsOverBundled(t *testing.T) {
	configured := map[string]controlapi.ExecutorEntry{
		"exec-a": {Transport: "grpc", Endpoint: "configured-host:9090", TLS: "required"},
	}
	bundled := map[string]executor.Endpoint{
		"exec-a": {Transport: "inproc", URL: "bundled-a"},
		"exec-b": {Transport: "inproc", URL: "bundled-b"},
	}

	mergeBundledExecutorEntries(configured, bundled)

	want := controlapi.ExecutorEntry{Transport: "grpc", Endpoint: "configured-host:9090", TLS: "required"}
	if got := configured["exec-a"]; got != want {
		t.Fatalf("exec-a was overwritten by bundled entry: got %+v, want %+v", got, want)
	}

	wantB := controlapi.ExecutorEntry{Transport: "inproc", Endpoint: "bundled-b"}
	if got := configured["exec-b"]; got != wantB {
		t.Fatalf("exec-b: got %+v, want %+v", got, wantB)
	}
}

func TestAdvertiseInto_SkipsNamesOverriddenByConfiguredEndpoint(t *testing.T) {
	regs := &BundledRegistrations{
		ExecutorAliases: map[string]executor.Endpoint{
			"exec-overridden":   {Transport: "inproc", URL: "bundled-overridden"},
			"exec-only-bundled": {Transport: "inproc", URL: "bundled-only"},
		},
		ExecutorAdverts: map[string]BundledExecutorAdvertisement{
			"exec-overridden":   {Schema: []byte(`{"bundled":true}`)},
			"exec-only-bundled": {Schema: []byte(`{"bundled":true}`)},
		},
		ClaimProducerAdverts: map[string]claimproducer.Capabilities{
			"store-overridden":   {},
			"store-only-bundled": {},
		},
	}

	disc := observability.NewDiscovery(nil)
	configuredExecutors := map[string]ExecutorEntry{
		"exec-overridden": {Transport: "grpc", Endpoint: "configured-host:9090"},
	}
	configuredClaimProducers := map[string]ClaimProducerEntry{
		"store-overridden": {Endpoint: "configured-store:9090"},
	}

	regs.AdvertiseInto(disc, configuredExecutors, configuredClaimProducers)

	if _, ok := disc.GetExecutor("exec-overridden"); ok {
		t.Fatalf("exec-overridden: bundled advert clobbered the configured executor's discovery entry")
	}
	if _, ok := disc.GetExecutor("exec-only-bundled"); !ok {
		t.Fatalf("exec-only-bundled: expected a discovery entry for the un-overridden bundled executor")
	}
	if _, ok := disc.GetClaimProducer("store-overridden"); ok {
		t.Fatalf("store-overridden: bundled advert clobbered the configured claim producer's discovery entry")
	}
	if _, ok := disc.GetClaimProducer("store-only-bundled"); !ok {
		t.Fatalf("store-only-bundled: expected a discovery entry for the un-overridden bundled claim producer")
	}
}
