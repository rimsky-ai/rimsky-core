// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package mcp

import (
	"encoding/json"
	"testing"
)

// TestPickCanonicalRoute pins the tool-aware route selection. A plain
// shortest-path heuristic mis-routes the `_list` tools (and even
// `_get` tools whose collection route is shorter); selection must honor
// both the args' satisfiable placeholders and the tool-name suffix.
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
	invalidateRoutes := []RegistryRoute{
		{Method: "POST", Path: "/nodes/{id}/invalidate"},
		{Method: "POST", Path: "/admin/instances/{instance}/nodes/{node_id}/invalidate"},
	}

	cases := []struct {
		name   string
		tool   string
		routes []RegistryRoute
		args   map[string]json.RawMessage
		want   string
	}{
		// @constraint: `node_list` supplies idOrKey → the by-instance collection route,
		// NOT the shorter by-id /nodes/{id}.
		{"node_list → by-instance collection", "node_list", nodeRoutes, withArgs("idOrKey"), "/instances/{idOrKey}/nodes"},
		{"node_get → by-id item", "node_get", nodeRoutes, withArgs("id"), "/nodes/{id}"},
		// @constraint: message_list and message_get BOTH supply `id`; only the suffix
		// disambiguates the otherwise-equally-satisfiable routes.
		{"message_list → instance messages", "message_list", msgRoutes, withArgs("id"), "/instances/{id}/messages"},
		{"message_get → message item", "message_get", msgRoutes, withArgs("id"), "/messages/{id}"},
		{"instance_list → collection", "instance_list", instRoutes, withArgs(), "/instances"},
		{"instance_get → item", "instance_get", instRoutes, withArgs("idOrKey"), "/instances/{idOrKey}"},
		{"template_list → collection", "template_list", tmplRoutes, withArgs(), "/templates"},
		{"template_get → item", "template_get", tmplRoutes, withArgs("id"), "/templates/{id}"},
		// @constraint: /admin/ variant is dropped even though it is not the shortest.
		{"node_invalidate skips admin", "node_invalidate", invalidateRoutes, withArgs("id"), "/nodes/{id}/invalidate"},
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

func TestIsItemRoute(t *testing.T) {
	cases := map[string]bool{
		"/nodes/{id}":                true,
		"/instances/{idOrKey}/nodes": false,
		"/instances":                 false,
		"/messages/{id}":             true,
		"/nodes/{id}/invalidate":     false,
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
