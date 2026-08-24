// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package lifecycle_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/lifecycle"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

// @concept: lifecycle-subscriber
func TestServicesReferencedBySpec_IncludesPublishers(t *testing.T) {
	t.Parallel()

	sp := spec.TemplateSpec{
		Name: "lifecycle-publishers", Version: "v1",
		Nodes: []spec.TemplateNodeDef{
			{Type: "n1", ClaimProducers: []spec.NodeClaimProducerRef{{Name: "alpha"}}},
			{Type: "n2", Executor: "beta"},
		},
		Publishers: []spec.PublisherSpec{
			{Name: "gamma", Kind: "cron"},
			{Name: "", Kind: "cron"},
			{Name: "alpha", Kind: "cron"},
		},
	}

	got := lifecycle.ServicesReferencedBySpec(sp)

	require.Equal(t, []string{"alpha", "beta", "gamma"}, got,
		"publisher-only services must be enumerated; empty names skipped; dedup with node refs preserved")
}

// @concept: host-daemon-proxy
func TestSubscribingServices_AppendsLateBindProxiesDedupedSkippingEmpty(t *testing.T) {
	t.Parallel()
	proxies := map[string]string{
		"svc-a": "gamma-proxy",
		"svc-b": "alpha",
		"svc-c": "",
	}
	sp := spec.TemplateSpec{
		Name: "late-bind-services", Version: "v1",
		Nodes: []spec.TemplateNodeDef{
			{Type: "n1", ClaimProducers: []spec.NodeClaimProducerRef{{Name: "alpha"}}},
			{Type: "n2", Executor: "beta"},
		},
		LateBindServices: []string{"svc-a"},
	}

	got := lifecycle.SubscribingServices(sp, proxies)

	require.Equal(t, []string{"alpha", "beta", "gamma-proxy"}, got,
		"a late-bind-declaring spec must append configured proxy services, deduped against spec-referenced services, skipping empty proxy names")
}

// @concept: host-daemon-proxy
func TestSubscribingServices_NoProxiesWhenSpecHasNoLateBindServices(t *testing.T) {
	t.Parallel()
	proxies := map[string]string{"svc-a": "gamma-proxy"}
	sp := spec.TemplateSpec{
		Name: "no-late-bind", Version: "v1",
		Nodes: []spec.TemplateNodeDef{{Type: "n1", Executor: "beta"}},
	}

	got := lifecycle.SubscribingServices(sp, proxies)

	require.Equal(t, []string{"beta"}, got,
		"a spec with no LateBindServices must not pull in any configured proxy services")
}

// @concept: host-daemon-proxy
func TestSubscribingServices_ProxyOrderDeterministicAcrossMultipleProxies(t *testing.T) {
	t.Parallel()
	proxies := map[string]string{
		"svc-z": "proxy-z",
		"svc-a": "proxy-a",
		"svc-m": "proxy-m",
	}
	sp := spec.TemplateSpec{
		Name: "late-bind-order", Version: "v1",
		LateBindServices: []string{"svc-a"},
	}

	want := lifecycle.SubscribingServices(sp, proxies)
	require.Equal(t, []string{"proxy-a", "proxy-m", "proxy-z"}, want,
		"proxies must be appended in sorted service-name order, not random map-iteration order")

	for i := 0; i < 20; i++ {
		got := lifecycle.SubscribingServices(sp, proxies)
		require.Equal(t, want, got,
			"proxy service order must be deterministic across repeated calls")
	}
}
