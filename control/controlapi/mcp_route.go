// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Mounting the in-control-api MCP protocol skin at POST /mcp. See
// spec section "MCP-as-skin" and the control/controlapi/mcp/ package
// for the JSON-RPC envelope and tool catalog.
//
// @concept: control-api

package controlapi

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/rimsky-ai/rimsky-core/control/controlapi/mcp"
	"github.com/rimsky-ai/rimsky-core/foundation/auth"
)

// Wire the cross-package hooks the mcp package consumes so it doesn't
// need to import controlapi (back-cycle). SetIdentityHook is the
// race-safe entry point that uses an atomic.Value under the hood;
// assigning the package-level var directly compiles but races under
// -race when tests touch the same var.
func init() {
	mcp.SetIdentityHook(func(ctx context.Context) (auth.Identity, bool) {
		return IdentityFromContextOK(ctx)
	})
	mcp.WithProtocolSkin = func(ctx context.Context, skin string) context.Context {
		return WithProtocolSkin(ctx, skin)
	}
}

// routerRef is a lazily-bound http.Handler — the catalog needs the
// chi router for in-process tool dispatch, but the router only
// exists once NewApp has finished registering all routes. We hand
// the catalog a routerRef whose .h is set after registration.
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

// mcpRouterRef is a per-NewApp routerRef. registerMCPRoute populates
// it with the catalog; NewApp's tail assigns the built router into
// rr.h so MCP tool calls can re-enter the chi pipeline.
//
// Currently the routerRef is per-call (NewApp creates one), but
// because the catalog closes over it, it lives for the life of the
// returned http.Handler.

func registerMCPRoute(r chi.Router, deps AppDeps) {
	if deps.AuthState == nil {
		return
	}
	rr := &routerRef{}
	// Stash the routerRef on the AppDeps via AuthState so NewApp's
	// tail can populate it after route registration completes.
	deps.AuthState.mcpRouterRef = rr
	catalog := &mcp.Catalog{
		Registry:    actionRegistryAdapter{deps.AuthState.Registry},
		Router:      rr,
		Description: descriptionForTool,
		Schemas:     builtinSchemas(),
	}
	// Resources skin: a parallel-shaped catalog for the breakpoint-hits
	// URI family. Per spec
	// .ok-planner/specs/2026-05-24-instance-debugger-design.md §6 the
	// only resource family v1 exposes is `rimsky://instances/{id}/
	// breakpoint-hits` and `rimsky://breakpoints/{bp_id}/hits`; the URI
	// scheme parsing lives in mcp_resources.go::parseBreakpointHitsURI
	// so this file stays a wiring layer.
	server := &mcp.Server{
		Tools:     catalog,
		Resources: newBreakpointResourceCatalog(deps),
	}
	// Gate /mcp itself with the `mcp:read` umbrella so initialize /
	// tools/list calls produce audit rows (per spec section "Audit
	// and dry-run" — every authenticated request lands in the event
	// log, including dry-runs and metadata calls). The per-tool gate
	// still runs for `tools/call` because Catalog.Invoke re-enters
	// the chi router through the routerRef; the umbrella's verb is
	// `read` so the `*:read` wildcard in the bundled `viewer` role
	// covers `tools/list` automatically.
	r.Post("/mcp", deps.AuthState.gateByAction("mcp:read", server.ServeHTTP))
}

// builtinSchemas returns per-tool input JSON schemas, mirroring the
// pre-spec standalone mcp-servers/control-api/tools.go shape so an
// MCP client (and the LLMs that drive them) sees the required
// arguments for each tool. Tools omitted from the map fall back to
// `{"type":"object"}` in the catalog — acceptable for argument-free
// list tools, but write tools need explicit shapes so the client can
// validate before round-tripping. Keep in lockstep with
// `v1Actions` in actions.go.
func builtinSchemas() map[string][]byte {
	obj := []byte(`{"type":"object","additionalProperties":true}`)
	return map[string][]byte{
		// Instances.
		"instance_list":      obj,
		"instance_get":       []byte(`{"type":"object","properties":{"idOrKey":{"type":"string","description":"instance id or instance_key"}},"required":["idOrKey"]}`),
		"instance_create":    []byte(`{"type":"object","properties":{"template":{"type":"string","description":"template tag or content hash"},"instance_key":{"type":"string"},"params":{"type":"object"},"attribute_overrides":{"type":"object"},"frame_delivery_mode":{"type":"string","enum":["serial_queue","coalesce"]}},"required":["template"]}`),
		"instance_terminate": []byte(`{"type":"object","properties":{"idOrKey":{"type":"string","description":"instance id or instance_key"}},"required":["idOrKey"]}`),
		"instance_pause":     []byte(`{"type":"object","properties":{"idOrKey":{"type":"string","description":"instance id or instance_key"}},"required":["idOrKey"]}`),
		"instance_resume":    []byte(`{"type":"object","properties":{"idOrKey":{"type":"string","description":"instance id or instance_key"}},"required":["idOrKey"]}`),

		// Breakpoints (concept:breakpoint — instance-debugger surface; spec §4).
		"breakpoint_list":       []byte(`{"type":"object","properties":{"idOrKey":{"type":"string","description":"instance id or instance_key"}},"required":["idOrKey"]}`),
		"breakpoint_create":     []byte(`{"type":"object","properties":{"idOrKey":{"type":"string","description":"instance id or instance_key"},"checkpoint":{"type":"string","enum":["before_dispatch","after_terminal"]},"matcher":{"type":"object"},"signal_type":{"type":"string","description":"only valid on after_terminal checkpoints"},"mode":{"type":"string","enum":["pause","notify_only"]},"overflow_policy":{"type":"string","enum":["drop_oldest","block_dispatch","auto_resume_after_ttl"]},"hit_ttl_seconds":{"type":"integer"},"ttl_seconds":{"type":"integer"}},"required":["idOrKey","checkpoint"]}`),
		"breakpoint_delete":     []byte(`{"type":"object","properties":{"idOrKey":{"type":"string","description":"instance id or instance_key"},"breakpoint_id":{"type":"string","description":"breakpoint UUID"}},"required":["idOrKey","breakpoint_id"]}`),
		"breakpoint_resume_hit": []byte(`{"type":"object","properties":{"idOrKey":{"type":"string","description":"instance id or instance_key"},"breakpoint_id":{"type":"string","description":"breakpoint UUID"},"hit_id":{"type":"string","description":"breakpoint hit UUID"},"overlay":{"type":"object","description":"optional one-shot attribute overlay"}},"required":["idOrKey","breakpoint_id","hit_id"]}`),

		// Templates.
		"template_list":       obj,
		"template_get":        []byte(`{"type":"object","properties":{"id":{"type":"string","description":"template tag or content hash"}},"required":["id"]}`),
		"template_register":   []byte(`{"type":"object","properties":{"spec":{"type":"object"},"tag":{"type":"string"},"source":{"type":"string"}},"required":["spec"]}`),
		"template_deploy":     []byte(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`),
		"template_undeploy":   []byte(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`),
		"template_deregister": []byte(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`),

		// Tags.
		"tag_list":   obj,
		"tag_create": []byte(`{"type":"object","properties":{"tag":{"type":"string"},"template":{"type":"string"}},"required":["tag","template"]}`),
		"tag_set":    []byte(`{"type":"object","properties":{"tag":{"type":"string"},"template":{"type":"string"}},"required":["tag","template"]}`),
		"tag_delete": []byte(`{"type":"object","properties":{"tag":{"type":"string"}},"required":["tag"]}`),

		// Nodes.
		"node_list":       []byte(`{"type":"object","properties":{"idOrKey":{"type":"string"}},"required":["idOrKey"]}`),
		"node_get":        []byte(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`),
		"node_invalidate": []byte(`{"type":"object","properties":{"id":{"type":"string"},"reason":{"type":"string"},"frame":{"type":"string","enum":["","in","next"]}},"required":["id"]}`),
		"node_reset":      []byte(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`),

		// Messages.
		"message_send": []byte(`{"type":"object","properties":{"id":{"type":"string","description":"instance id"},"kind":{"type":"string"},"target":{"type":"string"},"payload":{},"sender":{"type":"string"},"sender_kind":{"type":"string","enum":["operator","publisher"]},"publisher_subscription_id":{"type":"string"}},"required":["id","kind"]}`),
		"message_list": []byte(`{"type":"object","properties":{"id":{"type":"string","description":"instance id"}},"required":["id"]}`),
		"message_get":  []byte(`{"type":"object","properties":{"id":{"type":"string","description":"message id"}},"required":["id"]}`),

		// Events.
		"event_list": obj,

		// Lineage.
		"lineage_get":   []byte(`{"type":"object","properties":{"run_id":{"type":"string"},"claim_handle_id":{"type":"string"},"source_type":{"type":"string"},"source_id":{"type":"string"},"executor_name":{"type":"string"}}}`),
		"lineage_prune": []byte(`{"type":"object","properties":{"before":{"type":"string","description":"RFC3339 timestamp"}},"required":["before"]}`),

		// Parked nodes / wait-sets / claim holders.
		"parked_node_list":   []byte(`{"type":"object","properties":{"reason":{"type":"string"}}}`),
		"waitset_list":       obj,
		"claim_holders_list": []byte(`{"type":"object","properties":{"claim_handle_id":{"type":"string"}},"required":["claim_handle_id"]}`),

		// Backfills.
		"backfill_create":     []byte(`{"type":"object","properties":{"id":{"type":"string","description":"instance id"},"target_node":{"type":"string"},"partition_request_override":{},"reason":{"type":"string"}},"required":["id","target_node"]}`),
		"backfill_list":       []byte(`{"type":"object","properties":{"id":{"type":"string","description":"instance id"}},"required":["id"]}`),
		"backfill_get":        []byte(`{"type":"object","properties":{"op_id":{"type":"string"}},"required":["op_id"]}`),
		"backfill_partitions": []byte(`{"type":"object","properties":{"op_id":{"type":"string"}},"required":["op_id"]}`),
		"backfill_cancel":     []byte(`{"type":"object","properties":{"op_id":{"type":"string"}},"required":["op_id"]}`),

		// Assets.
		"asset_list":                    []byte(`{"type":"object","properties":{"id":{"type":"string","description":"instance id"}},"required":["id"]}`),
		"asset_get":                     []byte(`{"type":"object","properties":{"id":{"type":"string"},"alias":{"type":"string"}},"required":["id","alias"]}`),
		"asset_versions":                []byte(`{"type":"object","properties":{"id":{"type":"string"},"alias":{"type":"string"}},"required":["id","alias"]}`),
		"asset_materialization_history": []byte(`{"type":"object","properties":{"id":{"type":"string"},"alias":{"type":"string"}},"required":["id","alias"]}`),
		"asset_materialize":             []byte(`{"type":"object","properties":{"id":{"type":"string"},"alias":{"type":"string"},"payload":{},"reason":{"type":"string"}},"required":["id","alias"]}`),
		"asset_delete":                  []byte(`{"type":"object","properties":{"id":{"type":"string"},"alias":{"type":"string"}},"required":["id","alias"]}`),

		// Diagnostics.
		"held_frames_list": obj,

		// Auth.
		"auth_list":       obj,
		"auth_get":        []byte(`{"type":"object","properties":{"nameOrID":{"type":"string"}},"required":["nameOrID"]}`),
		"auth_status":     obj,
		"auth_create_key": []byte(`{"type":"object","properties":{"name":{"type":"string"},"permissions":{"type":"array"},"expires_at":{"type":"string"}},"required":["name","permissions"]}`),
		"auth_revoke_key": []byte(`{"type":"object","properties":{"nameOrID":{"type":"string"}},"required":["nameOrID"]}`),
		"auth_rotate_key": []byte(`{"type":"object","properties":{"nameOrID":{"type":"string"},"grace":{"type":"string"}},"required":["nameOrID"]}`),
	}
}

// descriptionForTool resolves a tool name to a human-readable
// description for tools/list. Looks up via the mcp.Registry.
func descriptionForTool(reg mcp.Registry, name string) string {
	if entry, ok := reg.EntryForTool(name); ok {
		if entry.Description != "" {
			return entry.Description
		}
		return entry.Action
	}
	return name
}

// actionRegistryAdapter adapts the controlapi.ActionRegistry to the
// mcp.Registry interface. The MCP package can't import controlapi
// (controlapi imports mcp, so a back-import would close a cycle);
// the adapter sits on the controlapi side and forwards the same
// methods through.
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
