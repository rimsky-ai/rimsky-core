// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Canonical action registry. Each action declares the HTTP routes
// (chi patterns) and MCP tools that map to it. Used by:
//
//   - the auth middleware to resolve an incoming request → action
//   - POST /auth/keys to reject grants referencing unknown actions
//   - the MCP tool catalog to filter tools per requesting key's grant
//
// See spec
// .ok-planner/specs/2026-05-15-control-plane-mcp-and-auth-design.md
// "Action grammar".
//
// @concept: permission

package controlapi

import (
	"fmt"
	"sort"
	"sync"

	"github.com/rimsky-ai/rimsky-core/foundation/auth"
)

// ActionEntry is one row in the canonical action registry.
type ActionEntry struct {
	Action   string   // e.g. "node:invalidate"
	IsWrite  bool     // false → read action; mode modifier ignored
	Routes   []Route  // HTTP routes that map to this action
	MCPTools []string // MCP tool names that map to this action

	// Description is human-readable text surfaced via the MCP tool
	// catalog (`tools/list`). Optional; defaults to the action
	// string when empty.
	Description string
}

// Route is one HTTP method+path pair (chi-template format).
type Route struct {
	Method string // e.g. "POST"
	Path   string // chi pattern, e.g. "/nodes/{id}/invalidate"
}

// ActionRegistry holds the canonical action list and the two
// lookup tables (route → action, MCP tool → action). Construct via
// NewActionRegistry; populate via Register; freeze with Build.
type ActionRegistry struct {
	mu      sync.RWMutex
	built   bool
	entries map[string]ActionEntry // by action string
	byRoute map[string]string      // "<METHOD> <pattern>" → action
	byTool  map[string]string      // MCP tool name → action
}

// NewActionRegistry returns an empty registry.
func NewActionRegistry() *ActionRegistry {
	return &ActionRegistry{
		entries: map[string]ActionEntry{},
		byRoute: map[string]string{},
		byTool:  map[string]string{},
	}
}

// Register adds one ActionEntry. Action strings must pass
// auth.ValidateActionString. Routes and tools must not collide with
// already-registered entries.
func (r *ActionRegistry) Register(e ActionEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.built {
		return fmt.Errorf("ActionRegistry already built; no further Register")
	}
	if err := auth.ValidateActionString(e.Action); err != nil {
		return fmt.Errorf("action %q: %w", e.Action, err)
	}
	if _, exists := r.entries[e.Action]; exists {
		return fmt.Errorf("action %q registered twice", e.Action)
	}
	for _, rt := range e.Routes {
		k := rt.Method + " " + rt.Path
		if prev, ok := r.byRoute[k]; ok {
			return fmt.Errorf("route %s collides between action %q and %q", k, prev, e.Action)
		}
		r.byRoute[k] = e.Action
	}
	for _, t := range e.MCPTools {
		if prev, ok := r.byTool[t]; ok {
			return fmt.Errorf("MCP tool %q collides between action %q and %q", t, prev, e.Action)
		}
		r.byTool[t] = e.Action
	}
	r.entries[e.Action] = e
	return nil
}

// Build freezes the registry. Required before lookups.
func (r *ActionRegistry) Build() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.built = true
}

// ActionForRoute returns the action for an HTTP method + chi route
// pattern, or "" if no action is registered. The caller passes the
// routed pattern (from chi's RouteContext().RoutePattern()), not the
// request URL.
func (r *ActionRegistry) ActionForRoute(method, pattern string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byRoute[method+" "+pattern]
}

// ActionForTool returns the action for an MCP tool name, or "".
func (r *ActionRegistry) ActionForTool(name string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byTool[name]
}

// IsKnownAction returns true if the exact action string is registered.
// Used by POST /auth/keys to reject grants referencing unknown actions.
func (r *ActionRegistry) IsKnownAction(action string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.entries[action]
	return ok
}

// Entry returns the full ActionEntry for an action string. Second
// return is false if the action is unknown.
func (r *ActionRegistry) Entry(action string) (ActionEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[action]
	return e, ok
}

// EntryForTool returns the ActionEntry registered for an MCP tool name.
// The second return is false if the tool is unknown.
func (r *ActionRegistry) EntryForTool(name string) (ActionEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.byTool[name]
	if !ok {
		return ActionEntry{}, false
	}
	e, ok := r.entries[a]
	return e, ok
}

// AllActions returns every registered action in deterministic order.
func (r *ActionRegistry) AllActions() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.entries))
	for a := range r.entries {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// AllTools returns every registered MCP tool name in deterministic
// order.
func (r *ActionRegistry) AllTools() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.byTool))
	for t := range r.byTool {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// BuildV1Registry returns the canonical V1 action registry frozen and
// ready for use. The list mirrors the spec's action grammar table;
// updates must be made here AND in the spec document.
func BuildV1Registry() *ActionRegistry {
	r := NewActionRegistry()
	for _, e := range v1Actions {
		if err := r.Register(e); err != nil {
			panic("BuildV1Registry: " + err.Error())
		}
	}
	r.Build()
	return r
}

// v1Actions is the canonical V1 action list. Mirror with the spec's
// action grammar table; updates in lockstep.
//
// `observability:read` is a supplemental action covering the
// `/v1/observability/*` read-only HTTP surface. It is not in the
// spec's table but is gated for consistency with "every endpoint is
// gated"; the bundled roles already cover it via `*:read`.
var v1Actions = []ActionEntry{
	// Instances
	{Action: "instance:read", IsWrite: false,
		Routes:      []Route{{"GET", "/instances"}, {"GET", "/instances/{idOrKey}"}},
		MCPTools:    []string{"instance_list", "instance_get"},
		Description: "Read instances; list all or get one by id-or-key."},
	{Action: "instance:create", IsWrite: true,
		Routes:      []Route{{"POST", "/instances"}},
		MCPTools:    []string{"instance_create"},
		Description: "Create a new instance from a template."},
	{Action: "instance:terminate", IsWrite: true,
		Routes:      []Route{{"DELETE", "/instances/{idOrKey}"}},
		MCPTools:    []string{"instance_terminate"},
		Description: "Terminate an instance."},
	{Action: "instance:pause", IsWrite: true,
		Routes:      []Route{{"POST", "/instances/{idOrKey}/pause"}},
		MCPTools:    []string{"instance_pause"},
		Description: "Soft-pause an instance; supervisor stops claiming new dispatches until resumed."},
	{Action: "instance:resume", IsWrite: true,
		Routes:      []Route{{"POST", "/instances/{idOrKey}/resume"}},
		MCPTools:    []string{"instance_resume"},
		Description: "Resume a paused instance; supervisor candidate-selection picks it up again."},

	// Breakpoints (concept:breakpoint — instance-debugger surface).
	{Action: "breakpoint:read", IsWrite: false,
		Routes:      []Route{{"GET", "/instances/{idOrKey}/breakpoints"}},
		MCPTools:    []string{"breakpoint_list"},
		Description: "List active breakpoints installed on an instance."},
	{Action: "breakpoint:create", IsWrite: true,
		Routes:      []Route{{"POST", "/instances/{idOrKey}/breakpoints"}},
		MCPTools:    []string{"breakpoint_create"},
		Description: "Install a runtime breakpoint on an instance."},
	{Action: "breakpoint:resume", IsWrite: true,
		Routes:      []Route{{"POST", "/instances/{idOrKey}/breakpoints/{breakpoint_id}/resume"}},
		MCPTools:    []string{"breakpoint_resume_hit"},
		Description: "Resume a paused breakpoint hit, optionally applying a one-shot attribute overlay."},
	{Action: "breakpoint:delete", IsWrite: true,
		Routes:      []Route{{"DELETE", "/instances/{idOrKey}/breakpoints/{breakpoint_id}"}},
		MCPTools:    []string{"breakpoint_delete"},
		Description: "Delete a breakpoint; cascades to its hits."},

	// Templates
	{Action: "template:read", IsWrite: false,
		Routes:      []Route{{"GET", "/templates"}, {"GET", "/templates/{id}"}},
		MCPTools:    []string{"template_list", "template_get"},
		Description: "Read templates; list all or get one by id."},
	{Action: "template:register", IsWrite: true,
		Routes:      []Route{{"POST", "/templates"}},
		MCPTools:    []string{"template_register"},
		Description: "Register (compile + persist) a new template."},
	{Action: "template:deploy", IsWrite: true,
		Routes:      []Route{{"POST", "/templates/{id}/deploy"}},
		MCPTools:    []string{"template_deploy"},
		Description: "Mark a template deployed; instances may use it."},
	{Action: "template:undeploy", IsWrite: true,
		Routes:      []Route{{"POST", "/templates/{id}/undeploy"}},
		MCPTools:    []string{"template_undeploy"},
		Description: "Mark a template undeployed; new instances rejected."},
	{Action: "template:deregister", IsWrite: true,
		Routes:      []Route{{"DELETE", "/templates/{id}"}},
		MCPTools:    []string{"template_deregister"},
		Description: "Delete a template; refused while any instance references it."},

	// Tags
	{Action: "tag:read", IsWrite: false,
		Routes:      []Route{{"GET", "/tags"}},
		MCPTools:    []string{"tag_list"},
		Description: "List template tags."},
	{Action: "tag:create", IsWrite: true,
		Routes:      []Route{{"POST", "/tags"}},
		MCPTools:    []string{"tag_create"},
		Description: "Create a new template tag."},
	{Action: "tag:set", IsWrite: true,
		Routes:      []Route{{"PUT", "/tags/{tag}"}},
		MCPTools:    []string{"tag_set"},
		Description: "Move a tag to point at a different template hash."},
	{Action: "tag:delete", IsWrite: true,
		Routes:      []Route{{"DELETE", "/tags/{tag}"}},
		MCPTools:    []string{"tag_delete"},
		Description: "Delete a template tag."},

	// Nodes
	{Action: "node:read", IsWrite: false,
		Routes:      []Route{{"GET", "/instances/{idOrKey}/nodes"}, {"GET", "/nodes/{id}"}},
		MCPTools:    []string{"node_list", "node_get"},
		Description: "Read nodes; list per instance or get by id."},
	{Action: "node:invalidate", IsWrite: true,
		Routes: []Route{
			{"POST", "/nodes/{id}/invalidate"},
			{"POST", "/admin/instances/{instance}/nodes/{node_id}/invalidate"},
		},
		MCPTools:    []string{"node_invalidate"},
		Description: "Invalidate a node (resumes if parked; otherwise marks stale + re-fires)."},
	{Action: "node:reset", IsWrite: true,
		Routes:      []Route{{"POST", "/nodes/{id}/reset"}},
		MCPTools:    []string{"node_reset"},
		Description: "Reset a failed node back to stale so it can be re-attempted."},

	// Messages
	{Action: "message:send", IsWrite: true,
		Routes:      []Route{{"POST", "/instances/{id}/messages"}},
		MCPTools:    []string{"message_send"},
		Description: "Send a message into an instance's message bus."},
	{Action: "message:read", IsWrite: false,
		Routes:      []Route{{"GET", "/instances/{id}/messages"}, {"GET", "/messages/{id}"}},
		MCPTools:    []string{"message_list", "message_get"},
		Description: "Read messages on an instance or by id."},

	// Events
	{Action: "event:read", IsWrite: false,
		Routes:      []Route{{"GET", "/events"}},
		MCPTools:    []string{"event_list"},
		Description: "Read the event log."},

	// Lineage
	{Action: "lineage:read", IsWrite: false,
		Routes: []Route{
			{"GET", "/lineage/runs/{run_id}"},
			{"GET", "/lineage/runs/{run_id}/ancestors"},
			{"GET", "/lineage/runs/{run_id}/descendants"},
			{"GET", "/lineage/claims/{claim_handle_id}"},
			{"GET", "/lineage/claims/{claim_handle_id}/ancestors"},
			{"GET", "/lineage/by-source/{source_type}/{source_id}"},
			{"GET", "/lineage/by-producer/{executor_name}"},
		},
		MCPTools:    []string{"lineage_get"},
		Description: "Read lineage graphs."},
	{Action: "lineage:prune", IsWrite: true,
		Routes:      []Route{{"POST", "/admin/lineage/prune"}},
		MCPTools:    []string{"lineage_prune"},
		Description: "Prune lineage rows older than a cutoff."},

	// Parked nodes
	{Action: "parked-node:read", IsWrite: false,
		Routes: []Route{
			{"GET", "/diagnostics/parked"},
			{"GET", "/admin/diagnostics/parked-nodes"},
		},
		MCPTools:    []string{"parked_node_list"},
		Description: "List nodes parked in the wait-set."},

	// Wait-sets
	{Action: "waitset:read", IsWrite: false,
		Routes:      []Route{{"GET", "/admin/diagnostics/wait-sets"}},
		MCPTools:    []string{"waitset_list"},
		Description: "List wait-set entries (sender/receiver edges)."},

	// Claim holders
	{Action: "claim-holders:read", IsWrite: false,
		Routes:      []Route{{"GET", "/lock-holders/{claim_handle_id}/claim-holders"}},
		MCPTools:    []string{"claim_holders_list"},
		Description: "List claim-holder rows for a claim handle."},

	// Backfills
	{Action: "backfill:create", IsWrite: true,
		Routes:      []Route{{"POST", "/instances/{id}/backfills"}},
		MCPTools:    []string{"backfill_create"},
		Description: "Start a backfill operation on an instance."},
	{Action: "backfill:read", IsWrite: false,
		Routes: []Route{
			{"GET", "/instances/{id}/backfills"},
			{"GET", "/backfills/{op_id}"},
			{"GET", "/backfills/{op_id}/partitions"},
		},
		MCPTools:    []string{"backfill_list", "backfill_get", "backfill_partitions"},
		Description: "Read backfills; list per instance or get by op_id."},
	{Action: "backfill:cancel", IsWrite: true,
		Routes:      []Route{{"POST", "/backfills/{op_id}/cancel"}},
		MCPTools:    []string{"backfill_cancel"},
		Description: "Cancel a running backfill operation."},

	// Assets
	{Action: "asset:read", IsWrite: false,
		Routes: []Route{
			{"GET", "/instances/{id}/assets"},
			{"GET", "/instances/{id}/assets/{alias}"},
			{"GET", "/instances/{id}/assets/{alias}/versions"},
			{"GET", "/instances/{id}/assets/{alias}/materialization-history"},
		},
		MCPTools:    []string{"asset_list", "asset_get", "asset_versions", "asset_materialization_history"},
		Description: "Read assets on an instance."},
	{Action: "asset:materialize", IsWrite: true,
		Routes:      []Route{{"POST", "/instances/{id}/assets/{alias}/materialize"}},
		MCPTools:    []string{"asset_materialize"},
		Description: "Materialize (re-compute) an asset version."},
	{Action: "asset:delete", IsWrite: true,
		Routes:      []Route{{"DELETE", "/instances/{id}/assets/{alias}"}},
		MCPTools:    []string{"asset_delete"},
		Description: "Delete an asset on an instance."},

	// Diagnostics
	{Action: "diagnostics:read", IsWrite: false,
		Routes:      []Route{{"GET", "/admin/diagnostics/held-frames"}},
		MCPTools:    []string{"held_frames_list"},
		Description: "List frames held by holding-subgraph claims."},

	// Auth (self-administration)
	{Action: "auth:read", IsWrite: false,
		Routes: []Route{
			{"GET", "/auth/keys"},
			{"GET", "/auth/keys/{nameOrID}"},
			{"GET", "/auth/status"},
		},
		MCPTools:    []string{"auth_list", "auth_get", "auth_status"},
		Description: "Read API-key state and the auth mode (anonymous|authenticated)."},
	{Action: "auth:create", IsWrite: true,
		Routes:      []Route{{"POST", "/auth/keys"}},
		MCPTools:    []string{"auth_create_key"},
		Description: "Mint a new API key. Plaintext surfaced exactly once."},
	{Action: "auth:revoke", IsWrite: true,
		Routes:      []Route{{"DELETE", "/auth/keys/{nameOrID}"}},
		MCPTools:    []string{"auth_revoke_key"},
		Description: "Revoke an API key. Refuses to leave zero active keys without force flag."},
	{Action: "auth:rotate", IsWrite: true,
		Routes:      []Route{{"POST", "/auth/keys/{nameOrID}/rotate"}},
		MCPTools:    []string{"auth_rotate_key"},
		Description: "Rotate an API key (mint a new plaintext with same identity; old key revoked at grace expiry)."},

	// Observability (HTTP-only; no MCP tool surface in V1).
	// Supplemental to the spec's action grammar table; covers the
	// `/v1/observability/*` read-only browse surface. Implicitly
	// covered by `*:read` in bundled roles.
	{Action: "observability:read", IsWrite: false,
		Routes:      []Route{{"GET", "/v1/observability/*"}},
		MCPTools:    nil,
		Description: "Read observability data via /v1/observability/*."},

	// MCP umbrella action covering the JSON-RPC dispatch surface at
	// `POST /mcp`. Gates `initialize` and `tools/list` (which run
	// inside the MCP Server without ever reaching tools/call). Tool
	// invocations (`tools/call`) re-enter the chi router through the
	// catalog and pick up the per-tool action gate there.
	//
	// Named `mcp:read` rather than `mcp:invoke` so the wildcard-
	// `*:read` bundled `viewer` role automatically covers
	// `tools/list`. The JSON-RPC dispatch surface is read-shaped at
	// the umbrella level — `tools/call` mutations re-gate against
	// the per-tool action and require those write permissions.
	{Action: "mcp:read", IsWrite: false,
		Routes:      []Route{{"POST", "/mcp"}},
		MCPTools:    nil,
		Description: "Invoke the MCP JSON-RPC dispatch surface; per-tool actions still gate tools/call."},
}
