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
	"strings"
	"sync"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
)

// ActionEntry is one row in the canonical action registry.
type ActionEntry struct {
	Action   string   // e.g. "node:invalidate"
	IsWrite  bool     // false → read action; mode modifier ignored
	Routes   []Route  // HTTP routes that map to this action
	MCPTools []string // MCP tool names that map to this action

	// ScopeDimensions enumerates the resource-selector keys this
	// action's `requestTargets` ever emits — i.e. the only `scope`
	// keys a grant entry for this action may carry. An empty list
	// means the action has no scopeable dimension; ValidateGrantScope
	// rejects any non-empty `scope` map on such an action so a grant
	// like `{"action": "auth:read", "scope": {"foo": "bar"}}` cannot
	// silently deny every request. Today the only dimension in use is
	// "template_tag" (for the template-lifecycle + tag + instance-create
	// actions in lib/control/controlapi/auth_request_target.go).
	ScopeDimensions []string

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

// ScopeDimensionsFor returns the registered scopeable dimensions for
// an exact (non-wildcard) action, along with ok=true. ok=false means
// the action is not in the registry — the caller decides whether to
// treat that as "unknown action" or to skip validation.
func (r *ActionRegistry) ScopeDimensionsFor(action string) ([]string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[action]
	if !ok {
		return nil, false
	}
	return e.ScopeDimensions, true
}

// ValidateGrantScope rejects any `scope` map key that the action does
// not declare in its ScopeDimensions. The action-grammar layer
// (auth.ValidateGrant) only checks the action string itself; the
// dimension list lives with the action entry so the check is per-
// action. Wildcard-action entries (`*`, `<noun>:*`, `*:<verb>`) are
// SKIPPED here — a wildcard entry can match many actions with
// different dimensions, so the per-action restriction wouldn't have a
// single "valid" set; the wildcard's scope is matched at request time
// against the routed action's dimension instead.
//
// Returns the first error encountered:
//   - "unknown scope dimension X for action Y; valid dimensions: [...]"
//     when the entry's scope carries a key not in Y's ScopeDimensions.
//   - "action Y does not support scope" when Y has no scopeable
//     dimensions (ScopeDimensions empty) but the entry's scope is
//     non-empty.
func (r *ActionRegistry) ValidateGrantScope(grant auth.Grant) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for i, e := range grant {
		// Wildcard entries cover many actions; per-action dimension
		// validation doesn't fit at mint time. The scope still has to
		// match SOMETHING at request time (auth.ScopeMatches will
		// reject any selector key the routed action doesn't emit), so
		// the worst case is the same denied-every-request failure mode
		// the unknown-dimension check protects exact actions from —
		// fix is to migrate the operator's wildcard entry into per-
		// action entries that this validator will then catch. We do
		// NOT enforce here.
		if e.Action == "*" || strings.HasSuffix(e.Action, ":*") || strings.HasPrefix(e.Action, "*:") {
			continue
		}
		entry, ok := r.entries[e.Action]
		if !ok {
			// Unknown action; the surrounding IsKnownAction check at
			// the mint handler is what 400s on that, so we leave it.
			continue
		}
		if len(e.Scope) == 0 {
			continue
		}
		if len(entry.ScopeDimensions) == 0 {
			return fmt.Errorf("grant entry %d: action %q does not support scope", i, e.Action)
		}
		valid := make(map[string]struct{}, len(entry.ScopeDimensions))
		for _, d := range entry.ScopeDimensions {
			valid[d] = struct{}{}
		}
		for k := range e.Scope {
			if _, ok := valid[k]; !ok {
				return fmt.Errorf("grant entry %d: unknown scope dimension %q for action %q; valid dimensions: %v",
					i, k, e.Action, entry.ScopeDimensions)
			}
		}
	}
	return nil
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
		Routes:          []Route{{"POST", "/instances"}},
		MCPTools:        []string{"instance_create"},
		ScopeDimensions: []string{"template_tag"},
		Description:     "Create a new instance from a template."},
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
	{Action: "instance:kill", IsWrite: true,
		Routes:      []Route{{"POST", "/instances/{idOrKey}/terminate"}},
		MCPTools:    []string{"instance_kill"},
		Description: "Force-terminate an instance: mark it terminal and abandon in-flight node-runs."},

	// Breakpoints (concept:breakpoint — instance-debugger surface).
	{Action: "breakpoint:read", IsWrite: false,
		Routes: []Route{
			{"GET", "/instances/{idOrKey}/breakpoints"},
			{"GET", "/instances/{idOrKey}/breakpoint-hits"},
		},
		MCPTools:    []string{"breakpoint_list"},
		Description: "List active breakpoints installed on an instance, or read its pending breakpoint hits."},
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
	{Action: "template:validate", IsWrite: false,
		Routes:      []Route{{"POST", "/templates/validate"}},
		MCPTools:    []string{"template_validate"},
		Description: "Validate a template spec without persisting; returns all validation findings."},
	{Action: "template:register", IsWrite: true,
		Routes:          []Route{{"POST", "/templates"}},
		MCPTools:        []string{"template_register"},
		ScopeDimensions: []string{"template_tag"},
		Description:     "Register (compile + persist) a new template."},
	{Action: "template:deploy", IsWrite: true,
		Routes:          []Route{{"POST", "/templates/{id}/deploy"}},
		MCPTools:        []string{"template_deploy"},
		ScopeDimensions: []string{"template_tag"},
		Description:     "Mark a template deployed; instances may use it."},
	{Action: "template:undeploy", IsWrite: true,
		Routes:          []Route{{"POST", "/templates/{id}/undeploy"}},
		MCPTools:        []string{"template_undeploy"},
		ScopeDimensions: []string{"template_tag"},
		Description:     "Mark a template undeployed; new instances rejected."},
	{Action: "template:deregister", IsWrite: true,
		Routes:          []Route{{"DELETE", "/templates/{id}"}},
		MCPTools:        []string{"template_deregister"},
		ScopeDimensions: []string{"template_tag"},
		Description:     "Delete a template; refused while any instance references it."},

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
		Routes:          []Route{{"PUT", "/tags/{tag}"}},
		MCPTools:        []string{"tag_set"},
		ScopeDimensions: []string{"template_tag"},
		Description:     "Move a tag to point at a different template hash."},
	{Action: "tag:delete", IsWrite: true,
		Routes:          []Route{{"DELETE", "/tags/{tag}"}},
		MCPTools:        []string{"tag_delete"},
		ScopeDimensions: []string{"template_tag"},
		Description:     "Delete a template tag."},

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

	// Audit (the auth.* slice of the event log; granted separately
	// from event:read because actor identity / IP / user-agent /
	// actions are sensitive — see concept:event-log, concept:permission).
	{Action: "audit:read", IsWrite: false,
		Routes:      []Route{{"GET", "/audit"}},
		MCPTools:    []string{"audit_list"},
		Description: "Read the auth audit log."},

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

	// Compose-origin marker. A "capability" action that no route or MCP
	// tool maps to — it is consulted by isComposeOrigin() to gate the
	// reserved `compose:` prefix bypass on tag-create / instance-create.
	// Only api-keys whose grant matches `compose:origin` (typically the
	// compose-CLI's privileged key) may create reserved-prefix names;
	// any other authenticated caller stamping the header alone lands on
	// the same reject path as an unmarked request. The empty Routes /
	// MCPTools lists keep this entry out of the route + tool tables; it
	// is reachable only through CheckGrant. IsWrite is false: the
	// action is a CAPABILITY MARKER (decides whether a sibling write —
	// tag:create / instance:create — accepts a reserved-prefix name);
	// it never directly mutates state itself, so it has no dry-run
	// branch to cover.
	// @concept: permission
	{Action: composeOriginAction, IsWrite: false,
		Routes:      nil,
		MCPTools:    nil,
		Description: "Capability marker for the privileged compose-CLI: grants the bearer the right to create reserved-prefix `compose:<project>:<...>` tags and instance keys. No route maps to this action; it is consulted server-side when a request stamps the X-Rimsky-Compose-Origin header."},

	// MCP umbrella action covering the MCP Streamable HTTP transport
	// surface at `/mcp`. `POST /mcp` carries the JSON-RPC dispatch:
	// gates `initialize` and `tools/list` (which run inside the MCP
	// Server without ever reaching tools/call); tool invocations
	// (`tools/call`) re-enter the chi router through the catalog and
	// pick up the per-tool action gate there. `GET /mcp` opens the
	// server-to-client SSE stream the default `type: http` client
	// probes on connect (idle in v1 — connect-and-control only, no live
	// push); it is gated under the same read umbrella so the probe lands
	// in the audit log like every other authenticated request.
	//
	// Named `mcp:read` rather than `mcp:invoke` so the wildcard-
	// `*:read` bundled `read-only` role automatically covers
	// `tools/list` and the GET stream. The JSON-RPC dispatch surface is
	// read-shaped at the umbrella level — `tools/call` mutations re-gate
	// against the per-tool action and require those write permissions.
	{Action: "mcp:read", IsWrite: false,
		Routes:      []Route{{"POST", "/mcp"}, {"GET", "/mcp"}},
		MCPTools:    nil,
		Description: "Invoke the MCP JSON-RPC dispatch surface (POST) and open the server-to-client stream (GET); per-tool actions still gate tools/call."},
}
