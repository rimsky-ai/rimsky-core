// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: control-api

package controlapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/rimsky-ai/rimsky-core/lib/control/controlapi/mcp"
)

type routerRef struct {
	h http.Handler
}

func (rr *routerRef) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if rr.h == nil {
		http.Error(w, "mcp: router not wired", http.StatusInternalServerError)
		return
	}
	rr.h.ServeHTTP(w, r)
}

func registerMCPRoute(r chi.Router, deps AppDeps) {
	if deps.AuthState == nil {
		return
	}
	rr := &routerRef{}
	deps.AuthState.mcpRouterRef = rr
	catalog := &mcp.Catalog{
		Registry:         actionRegistryAdapter{deps.AuthState.Registry},
		Router:           rr,
		Description:      descriptionForTool,
		Schemas:          builtinSchemas(),
		ResolveIdentity:  IdentityFromContextOK,
		WithProtocolSkin: WithProtocolSkin,
	}
	server := &mcp.Server{
		Tools:     catalog,
		Resources: newBreakpointResourceCatalog(deps),
	}
	gated := deps.AuthState.gateByAction("mcp:read", server.ServeHTTP)
	r.Post("/mcp", gated)
	r.Get("/mcp", gated)
}

func builtinSchemas() map[string][]byte {
	obj := []byte(`{"type":"object","additionalProperties":true}`)
	return map[string][]byte{
		"instance_list":      obj,
		"instance_get":       []byte(`{"type":"object","properties":{"idOrKey":{"type":"string","description":"instance id or instance_key"}},"required":["idOrKey"]}`),
		"instance_create":    []byte(`{"type":"object","properties":{"template":{"type":"string","description":"template tag or content hash"},"instance_key":{"type":"string"},"params":{"type":"object"},"attribute_overrides":{"type":"object"}},"required":["template"]}`),
		"instance_terminate": []byte(`{"type":"object","properties":{"idOrKey":{"type":"string","description":"instance id or instance_key"}},"required":["idOrKey"]}`),
		"instance_pause":     []byte(`{"type":"object","properties":{"idOrKey":{"type":"string","description":"instance id or instance_key"}},"required":["idOrKey"]}`),
		"instance_resume":    []byte(`{"type":"object","properties":{"idOrKey":{"type":"string","description":"instance id or instance_key"}},"required":["idOrKey"]}`),
		"instance_kill":      []byte(`{"type":"object","properties":{"idOrKey":{"type":"string","description":"instance id or instance_key"},"reason":{"type":"string","description":"optional reason recorded on the teardown audit event"}},"required":["idOrKey"]}`),
		// @decision: debug-channel-gate-paused-or-breakpoint
		"instance_debug_override": []byte(`{"type":"object","properties":{"id":{"type":"string","description":"instance id"},"action":{"type":"string","enum":["invalidate_node","set_attribute"]},"node_type":{"type":"string"},"attribute_key":{"type":"string"},"attribute_value":{}},"required":["id","action","node_type"]}`),

		// @concept: breakpoint
		"breakpoint_list":       []byte(`{"type":"object","properties":{"idOrKey":{"type":"string","description":"instance id or instance_key"}},"required":["idOrKey"]}`),
		"breakpoint_create":     []byte(`{"type":"object","properties":{"idOrKey":{"type":"string","description":"instance id or instance_key"},"checkpoint":{"type":"string","enum":["before_dispatch","after_terminal"]},"matcher":{"type":"object"},"signal_type":{"type":"string","description":"only valid on after_terminal checkpoints"},"mode":{"type":"string","enum":["pause","notify_only"]},"overflow_policy":{"type":"string","enum":["drop_oldest","block_dispatch","auto_resume_after_ttl"]},"hit_ttl_seconds":{"type":"integer"},"ttl_seconds":{"type":"integer"}},"required":["idOrKey","checkpoint"]}`),
		"breakpoint_delete":     []byte(`{"type":"object","properties":{"idOrKey":{"type":"string","description":"instance id or instance_key"},"breakpoint_id":{"type":"string","description":"breakpoint UUID"}},"required":["idOrKey","breakpoint_id"]}`),
		"breakpoint_resume_hit": []byte(`{"type":"object","properties":{"idOrKey":{"type":"string","description":"instance id or instance_key"},"breakpoint_id":{"type":"string","description":"breakpoint UUID"},"hit_id":{"type":"string","description":"breakpoint hit UUID"},"overlay":{"type":"object","description":"optional one-shot attribute overlay"}},"required":["idOrKey","breakpoint_id","hit_id"]}`),

		"template_list":       obj,
		"template_get":        []byte(`{"type":"object","properties":{"id":{"type":"string","description":"template tag or content hash"}},"required":["id"]}`),
		"template_validate":   []byte(`{"type":"object","properties":{"spec":{"type":"object"}},"required":["spec"]}`),
		"template_register":   []byte(`{"type":"object","properties":{"spec":{"type":"object"},"tag":{"type":"string"},"source":{"type":"string"}},"required":["spec"]}`),
		"template_deploy":     []byte(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`),
		"template_undeploy":   []byte(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`),
		"template_deregister": []byte(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`),

		"tag_list":   obj,
		"tag_create": []byte(`{"type":"object","properties":{"tag":{"type":"string"},"template":{"type":"string"}},"required":["tag","template"]}`),
		"tag_set":    []byte(`{"type":"object","properties":{"tag":{"type":"string"},"template":{"type":"string"}},"required":["tag","template"]}`),
		"tag_delete": []byte(`{"type":"object","properties":{"tag":{"type":"string"}},"required":["tag"]}`),

		"node_list":  []byte(`{"type":"object","properties":{"idOrKey":{"type":"string"}},"required":["idOrKey"]}`),
		"node_get":   []byte(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`),
		"node_reset": []byte(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`),

		"message_send": []byte(`{"type":"object","properties":{"id":{"type":"string","description":"instance id"},"type":{"type":"string"},"payload":{},"sender":{"type":"string"},"publisher_subscription_id":{"type":"string","description":"if set, request is treated as a publisher emit; otherwise as an operator emit"}},"required":["id","type"]}`),
		"message_list": []byte(`{"type":"object","properties":{"id":{"type":"string","description":"instance id"}},"required":["id"]}`),
		"message_get":  []byte(`{"type":"object","properties":{"id":{"type":"string","description":"message id"}},"required":["id"]}`),

		"event_list": obj,

		"lineage_get":   []byte(`{"type":"object","properties":{"run_id":{"type":"string"},"claim_handle_id":{"type":"string"},"source_type":{"type":"string"},"source_id":{"type":"string"},"executor_name":{"type":"string"}}}`),
		"lineage_prune": []byte(`{"type":"object","properties":{"before":{"type":"string","description":"RFC3339 timestamp"}},"required":["before"]}`),

		"parked_node_list":   []byte(`{"type":"object","properties":{"reason":{"type":"string"}}}`),
		"waitset_list":       obj,
		"claim_holders_list": []byte(`{"type":"object","properties":{"claim_handle_id":{"type":"string"}},"required":["claim_handle_id"]}`),

		"asset_list":                    []byte(`{"type":"object","properties":{"id":{"type":"string","description":"instance id"}},"required":["id"]}`),
		"asset_get":                     []byte(`{"type":"object","properties":{"id":{"type":"string"},"alias":{"type":"string"}},"required":["id","alias"]}`),
		"asset_versions":                []byte(`{"type":"object","properties":{"id":{"type":"string"},"alias":{"type":"string"}},"required":["id","alias"]}`),
		"asset_materialization_history": []byte(`{"type":"object","properties":{"id":{"type":"string"},"alias":{"type":"string"}},"required":["id","alias"]}`),
		"asset_delete":                  []byte(`{"type":"object","properties":{"id":{"type":"string"},"alias":{"type":"string"}},"required":["id","alias"]}`),

		"held_frames_list": obj,

		"auth_list":       obj,
		"auth_get":        []byte(`{"type":"object","properties":{"nameOrID":{"type":"string"}},"required":["nameOrID"]}`),
		"auth_status":     obj,
		"auth_create_key": []byte(`{"type":"object","properties":{"name":{"type":"string"},"permissions":{"type":"array"},"expires_at":{"type":"string"}},"required":["name","permissions"]}`),
		"auth_revoke_key": []byte(`{"type":"object","properties":{"nameOrID":{"type":"string"}},"required":["nameOrID"]}`),
		"auth_rotate_key": []byte(`{"type":"object","properties":{"nameOrID":{"type":"string"},"grace":{"type":"string"}},"required":["nameOrID"]}`),
	}
}

func descriptionForTool(reg mcp.Registry, name string) string {
	if entry, ok := reg.EntryForTool(name); ok {
		if entry.Description != "" {
			return entry.Description
		}
		return entry.Action
	}
	return name
}

type actionRegistryAdapter struct{ inner *ActionRegistry }

func (a actionRegistryAdapter) AllTools() []string { return a.inner.AllTools() }
func (a actionRegistryAdapter) EntryForTool(name string) (mcp.RegistryEntry, bool) {
	e, ok := a.inner.EntryForTool(name)
	if !ok {
		return mcp.RegistryEntry{}, false
	}
	routes := make([]mcp.RegistryRoute, 0, len(e.Routes))
	for _, r := range e.Routes {
		routes = append(routes, mcp.RegistryRoute{Method: r.Method, Path: r.Path})
	}
	return mcp.RegistryEntry{
		Action:      e.Action,
		IsWrite:     e.IsWrite,
		Routes:      routes,
		Description: e.Description,
	}, true
}
