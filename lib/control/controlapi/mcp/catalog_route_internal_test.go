// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package mcp

import (
	"encoding/json"
	"testing"
)

func TestPickCanonicalRoute(t *testing.T) {
	withArgs := func(keys ...string) map[string]json.RawMessage {
		m := map[string]json.RawMessage{}
		for _, k := range keys {
			m[k] = json.RawMessage(`"x"`)
		}
		return m
	}

	nodeRoutes := []RegistryRoute{
		{Method: "GET", Path: "/instances/{idOrKey}/nodes"},
		{Method: "GET", Path: "/nodes/{id}"},
	}
	msgRoutes := []RegistryRoute{
		{Method: "GET", Path: "/instances/{id}/messages"},
		{Method: "GET", Path: "/messages/{id}"},
	}
	instRoutes := []RegistryRoute{
		{Method: "GET", Path: "/instances"},
		{Method: "GET", Path: "/instances/{idOrKey}"},
	}
	tmplRoutes := []RegistryRoute{
		{Method: "GET", Path: "/templates"},
		{Method: "GET", Path: "/templates/{id}"},
	}

	cases := []struct {
		name   string
		tool   string
		routes []RegistryRoute
		args   map[string]json.RawMessage
		want   string
	}{
		{"node_list → by-instance collection", "node_list", nodeRoutes, withArgs("idOrKey"), "/instances/{idOrKey}/nodes"},
		{"node_get → by-id item", "node_get", nodeRoutes, withArgs("id"), "/nodes/{id}"},
		{"message_list → instance messages", "message_list", msgRoutes, withArgs("id"), "/instances/{id}/messages"},
		{"message_get → message item", "message_get", msgRoutes, withArgs("id"), "/messages/{id}"},
		{"instance_list → collection", "instance_list", instRoutes, withArgs(), "/instances"},
		{"instance_get → item", "instance_get", instRoutes, withArgs("idOrKey"), "/instances/{idOrKey}"},
		{"template_list → collection", "template_list", tmplRoutes, withArgs(), "/templates"},
		{"template_get → item", "template_get", tmplRoutes, withArgs("id"), "/templates/{id}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pickCanonicalRoute(tc.tool, tc.routes, tc.args)
			if got.Path != tc.want {
				t.Fatalf("pickCanonicalRoute(%q) path = %q, want %q", tc.tool, got.Path, tc.want)
			}
		})
	}
}

func TestPickCanonicalRoute_RealActionRouteSets(t *testing.T) {
	withArgs := func(keys ...string) map[string]json.RawMessage {
		m := map[string]json.RawMessage{}
		for _, k := range keys {
			m[k] = json.RawMessage(`"x"`)
		}
		return m
	}

	authReadRoutes := []RegistryRoute{
		{Method: "GET", Path: "/v1/auth/keys", Tool: "auth_list"},
		{Method: "GET", Path: "/v1/auth/keys/{nameOrID}", Tool: "auth_get"},
		{Method: "GET", Path: "/v1/auth/status", Tool: "auth_status"},
	}
	assetReadRoutes := []RegistryRoute{
		{Method: "GET", Path: "/v1/instances/{id}/assets", Tool: "asset_list"},
		{Method: "GET", Path: "/v1/instances/{id}/assets/{alias}", Tool: "asset_get"},
		{Method: "GET", Path: "/v1/instances/{id}/assets/{alias}/versions", Tool: "asset_versions"},
		{Method: "GET", Path: "/v1/instances/{id}/assets/{alias}/materialization-history", Tool: "asset_materialization_history"},
	}
	lineageReadRoutes := []RegistryRoute{
		{Method: "GET", Path: "/v1/lineage/runs/{run_id}"},
		{Method: "GET", Path: "/v1/lineage/runs/{run_id}/ancestors", Tool: "lineage_run_ancestors"},
		{Method: "GET", Path: "/v1/lineage/runs/{run_id}/descendants", Tool: "lineage_run_descendants"},
		{Method: "GET", Path: "/v1/lineage/claims/{claim_handle_id}"},
		{Method: "GET", Path: "/v1/lineage/claims/{claim_handle_id}/ancestors", Tool: "lineage_claim_ancestors"},
		{Method: "GET", Path: "/v1/lineage/claims/{claim_handle_id}/descendants", Tool: "lineage_claim_descendants"},
		{Method: "GET", Path: "/v1/lineage/by-source/{source_type}/{source_id}"},
		{Method: "GET", Path: "/v1/lineage/by-producer/{executor_name}"},
	}
	parkedNodeReadRoutes := []RegistryRoute{
		{Method: "GET", Path: "/v1/admin/diagnostics/parked-nodes"},
	}

	cases := []struct {
		name   string
		tool   string
		routes []RegistryRoute
		args   map[string]json.RawMessage
		want   string
	}{
		{"auth_list → collection", "auth_list", authReadRoutes, withArgs(), "/v1/auth/keys"},
		{"auth_get → item", "auth_get", authReadRoutes, withArgs("nameOrID"), "/v1/auth/keys/{nameOrID}"},
		{"auth_status → status, not the keys collection", "auth_status", authReadRoutes, withArgs(), "/v1/auth/status"},

		{"asset_list → collection", "asset_list", assetReadRoutes, withArgs("id"), "/v1/instances/{id}/assets"},
		{"asset_get → item", "asset_get", assetReadRoutes, withArgs("id", "alias"), "/v1/instances/{id}/assets/{alias}"},
		{"asset_versions → versions sub-route, not the asset item", "asset_versions", assetReadRoutes, withArgs("id", "alias"), "/v1/instances/{id}/assets/{alias}/versions"},
		{"asset_materialization_history → history sub-route, not the asset item", "asset_materialization_history", assetReadRoutes, withArgs("id", "alias"), "/v1/instances/{id}/assets/{alias}/materialization-history"},

		{"lineage_get by run_id → plain run lookup", "lineage_get", lineageReadRoutes, withArgs("run_id"), "/v1/lineage/runs/{run_id}"},
		{"lineage_get by claim_handle_id → plain claim lookup", "lineage_get", lineageReadRoutes, withArgs("claim_handle_id"), "/v1/lineage/claims/{claim_handle_id}"},
		{"lineage_get by source_type+source_id → by-source reverse lookup", "lineage_get", lineageReadRoutes, withArgs("source_type", "source_id"), "/v1/lineage/by-source/{source_type}/{source_id}"},
		{"lineage_get by executor_name → by-producer reverse lookup", "lineage_get", lineageReadRoutes, withArgs("executor_name"), "/v1/lineage/by-producer/{executor_name}"},
		{"lineage_run_ancestors → ancestors sub-route, not the run item", "lineage_run_ancestors", lineageReadRoutes, withArgs("run_id"), "/v1/lineage/runs/{run_id}/ancestors"},
		{"lineage_run_descendants → descendants sub-route, not the run item", "lineage_run_descendants", lineageReadRoutes, withArgs("run_id"), "/v1/lineage/runs/{run_id}/descendants"},
		{"lineage_claim_ancestors → ancestors sub-route, not the claim item", "lineage_claim_ancestors", lineageReadRoutes, withArgs("claim_handle_id"), "/v1/lineage/claims/{claim_handle_id}/ancestors"},
		{"lineage_claim_descendants → descendants sub-route, not the claim item", "lineage_claim_descendants", lineageReadRoutes, withArgs("claim_handle_id"), "/v1/lineage/claims/{claim_handle_id}/descendants"},

		{"parked_node_list → the sole admin route", "parked_node_list", parkedNodeReadRoutes, withArgs(), "/v1/admin/diagnostics/parked-nodes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pickCanonicalRoute(tc.tool, tc.routes, tc.args)
			if got.Path != tc.want {
				t.Fatalf("pickCanonicalRoute(%q) path = %q, want %q", tc.tool, got.Path, tc.want)
			}
		})
	}
}

func TestPickCanonicalRoute_AdminVariantDeprioritizedEvenWhenShorter(t *testing.T) {
	routes := []RegistryRoute{
		{Method: "GET", Path: "/v1/admin/x"},
		{Method: "GET", Path: "/v1/diagnostics/x-non-admin-and-longer"},
	}
	got := pickCanonicalRoute("x_list", routes, map[string]json.RawMessage{})
	if got.Path != "/v1/diagnostics/x-non-admin-and-longer" {
		t.Fatalf("pickCanonicalRoute picked the /v1/admin/ variant %q; the non-admin variant must win regardless of path length", got.Path)
	}
}

func TestIsItemRoute(t *testing.T) {
	cases := map[string]bool{
		"/nodes/{id}":                true,
		"/instances/{idOrKey}/nodes": false,
		"/instances":                 false,
		"/messages/{id}":             true,
		"/nodes/{id}/reset":          false,
	}
	for path, want := range cases {
		if got := isItemRoute(path); got != want {
			t.Errorf("isItemRoute(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestPathPlaceholders(t *testing.T) {
	got := pathPlaceholders("/instances/{idOrKey}/breakpoints/{breakpoint_id}")
	if len(got) != 2 || got[0] != "idOrKey" || got[1] != "breakpoint_id" {
		t.Fatalf("pathPlaceholders = %v, want [idOrKey breakpoint_id]", got)
	}
	if ph := pathPlaceholders("/v1/observability/*"); len(ph) != 0 {
		t.Fatalf("wildcard path should yield no placeholders, got %v", ph)
	}
}
