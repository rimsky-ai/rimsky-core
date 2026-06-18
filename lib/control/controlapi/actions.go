// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: permission

package controlapi

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/auth"
)

type ActionEntry struct {
	Action   string
	IsWrite  bool
	Routes   []Route
	MCPTools []string

	ScopeDimensions []string

	Description string
}

type Route struct {
	Method string
	Path   string
}

type ActionRegistry struct {
	mu      sync.RWMutex
	built   bool
	entries map[string]ActionEntry
	byRoute map[string]string
	byTool  map[string]string
}

func NewActionRegistry() *ActionRegistry {
	return &ActionRegistry{
		entries: map[string]ActionEntry{},
		byRoute: map[string]string{},
		byTool:  map[string]string{},
	}
}

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

func (r *ActionRegistry) Build() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.built = true
}

func (r *ActionRegistry) ActionForRoute(method, pattern string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byRoute[method+" "+pattern]
}

func (r *ActionRegistry) ActionForTool(name string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byTool[name]
}

func (r *ActionRegistry) IsKnownAction(action string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.entries[action]
	return ok
}

func (r *ActionRegistry) ScopeDimensionsFor(action string) ([]string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[action]
	if !ok {
		return nil, false
	}
	return e.ScopeDimensions, true
}

func (r *ActionRegistry) ValidateGrantScope(grant auth.Grant) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for i, e := range grant {
		if e.Action == "*" || strings.HasSuffix(e.Action, ":*") || strings.HasPrefix(e.Action, "*:") {
			continue
		}
		entry, ok := r.entries[e.Action]
		if !ok {
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

func (r *ActionRegistry) Entry(action string) (ActionEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[action]
	return e, ok
}

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

var v1Actions = []ActionEntry{
	{Action: "instance:read", IsWrite: false,
		Routes:      []Route{{"GET", "/v1/instances"}, {"GET", "/v1/instances/{idOrKey}"}},
		MCPTools:    []string{"instance_list", "instance_get"},
		Description: "Read instances; list all or get one by id-or-key."},
	{Action: "instance:create", IsWrite: true,
		Routes:          []Route{{"POST", "/v1/instances"}},
		MCPTools:        []string{"instance_create"},
		ScopeDimensions: []string{"template_tag"},
		Description:     "Create a new instance from a template."},
	{Action: "instance:terminate", IsWrite: true,
		Routes:      []Route{{"DELETE", "/v1/instances/{idOrKey}"}},
		MCPTools:    []string{"instance_terminate"},
		Description: "Terminate an instance."},
	{Action: "instance:pause", IsWrite: true,
		Routes:      []Route{{"POST", "/v1/instances/{idOrKey}/pause"}},
		MCPTools:    []string{"instance_pause"},
		Description: "Soft-pause an instance; supervisor stops claiming new dispatches until resumed."},
	{Action: "instance:resume", IsWrite: true,
		Routes:      []Route{{"POST", "/v1/instances/{idOrKey}/resume"}},
		MCPTools:    []string{"instance_resume"},
		Description: "Resume a paused instance; supervisor candidate-selection picks it up again."},
	{Action: "instance:kill", IsWrite: true,
		Routes:      []Route{{"POST", "/v1/instances/{idOrKey}/terminate"}},
		MCPTools:    []string{"instance_kill"},
		Description: "Force-terminate an instance: mark it terminal and abandon in-flight node-runs."},
	{Action: "instance:debug-override", IsWrite: true,
		Routes:      []Route{{"POST", "/v1/instances/{id}/debug/override"}},
		MCPTools:    []string{"instance_debug_override"},
		Description: "Apply an ad-hoc debug override (invalidate_node or set_attribute) on a paused or breakpoint-held instance."},

	// @concept: cascade-graph — frames-read surface. The
	// triggering_message_id filter on the list endpoint surfaces the
	// message → frames reverse join the frame-origin-audit story
	// consumes.
	{Action: "instance:list-frames", IsWrite: false,
		Routes:      []Route{{"GET", "/v1/instances/{id}/frames"}},
		Description: "List frames for an instance; supports filter by triggering_message_id."},
	{Action: "instance:read-frame", IsWrite: false,
		Routes:      []Route{{"GET", "/v1/instances/{id}/frames/{frame_id}"}},
		Description: "Fetch one frame joined with its triggering message envelope."},

	// @concept: breakpoint — instance-debugger surface.
	{Action: "breakpoint:read", IsWrite: false,
		Routes: []Route{
			{"GET", "/v1/instances/{idOrKey}/breakpoints"},
			{"GET", "/v1/instances/{idOrKey}/breakpoint-hits"},
		},
		MCPTools:    []string{"breakpoint_list"},
		Description: "List active breakpoints installed on an instance, or read its pending breakpoint hits."},
	{Action: "breakpoint:create", IsWrite: true,
		Routes:      []Route{{"POST", "/v1/instances/{idOrKey}/breakpoints"}},
		MCPTools:    []string{"breakpoint_create"},
		Description: "Install a runtime breakpoint on an instance."},
	{Action: "breakpoint:resume", IsWrite: true,
		Routes:      []Route{{"POST", "/v1/instances/{idOrKey}/breakpoints/{breakpoint_id}/resume"}},
		MCPTools:    []string{"breakpoint_resume_hit"},
		Description: "Resume a paused breakpoint hit, optionally applying a one-shot attribute overlay."},
	{Action: "breakpoint:delete", IsWrite: true,
		Routes:      []Route{{"DELETE", "/v1/instances/{idOrKey}/breakpoints/{breakpoint_id}"}},
		MCPTools:    []string{"breakpoint_delete"},
		Description: "Delete a breakpoint; cascades to its hits."},

	{Action: "template:read", IsWrite: false,
		Routes:      []Route{{"GET", "/v1/templates"}, {"GET", "/v1/templates/{id}"}},
		MCPTools:    []string{"template_list", "template_get"},
		Description: "Read templates; list all or get one by id."},
	{Action: "template:validate", IsWrite: false,
		Routes:      []Route{{"POST", "/v1/templates/validate"}},
		MCPTools:    []string{"template_validate"},
		Description: "Validate a template spec without persisting; returns all validation findings."},
	{Action: "template:register", IsWrite: true,
		Routes:          []Route{{"POST", "/v1/templates"}},
		MCPTools:        []string{"template_register"},
		ScopeDimensions: []string{"template_tag"},
		Description:     "Register (compile + persist) a new template."},
	{Action: "template:deploy", IsWrite: true,
		Routes:          []Route{{"POST", "/v1/templates/{id}/deploy"}},
		MCPTools:        []string{"template_deploy"},
		ScopeDimensions: []string{"template_tag"},
		Description:     "Mark a template deployed; instances may use it."},
	{Action: "template:undeploy", IsWrite: true,
		Routes:          []Route{{"POST", "/v1/templates/{id}/undeploy"}},
		MCPTools:        []string{"template_undeploy"},
		ScopeDimensions: []string{"template_tag"},
		Description:     "Mark a template undeployed; new instances rejected."},
	{Action: "template:deregister", IsWrite: true,
		Routes:          []Route{{"DELETE", "/v1/templates/{id}"}},
		MCPTools:        []string{"template_deregister"},
		ScopeDimensions: []string{"template_tag"},
		Description:     "Delete a template; refused while any instance references it."},

	{Action: "tag:read", IsWrite: false,
		Routes:      []Route{{"GET", "/v1/tags"}},
		MCPTools:    []string{"tag_list"},
		Description: "List template tags."},
	{Action: "tag:create", IsWrite: true,
		Routes:      []Route{{"POST", "/v1/tags"}},
		MCPTools:    []string{"tag_create"},
		Description: "Create a new template tag."},
	{Action: "tag:set", IsWrite: true,
		Routes:          []Route{{"PUT", "/v1/tags/{tag}"}},
		MCPTools:        []string{"tag_set"},
		ScopeDimensions: []string{"template_tag"},
		Description:     "Move a tag to point at a different template hash."},
	{Action: "tag:delete", IsWrite: true,
		Routes:          []Route{{"DELETE", "/v1/tags/{tag}"}},
		MCPTools:        []string{"tag_delete"},
		ScopeDimensions: []string{"template_tag"},
		Description:     "Delete a template tag."},

	{Action: "node:read", IsWrite: false,
		Routes:      []Route{{"GET", "/v1/instances/{idOrKey}/nodes"}, {"GET", "/v1/nodes/{id}"}},
		MCPTools:    []string{"node_list", "node_get"},
		Description: "Read nodes; list per instance or get by id."},
	{Action: "node:reset", IsWrite: true,
		Routes:      []Route{{"POST", "/v1/nodes/{id}/reset"}},
		MCPTools:    []string{"node_reset"},
		Description: "Reset a failed node back to stale so it can be re-attempted."},

	{Action: "message:send", IsWrite: true,
		Routes:      []Route{{"POST", "/v1/instances/{id}/messages"}},
		MCPTools:    []string{"message_send"},
		Description: "Send a message into an instance's message bus."},
	{Action: "message:read", IsWrite: false,
		Routes:      []Route{{"GET", "/v1/instances/{id}/messages"}, {"GET", "/v1/messages/{id}"}},
		MCPTools:    []string{"message_list", "message_get"},
		Description: "Read messages on an instance or by id."},

	{Action: "event:read", IsWrite: false,
		Routes:      []Route{{"GET", "/v1/events"}},
		MCPTools:    []string{"event_list"},
		Description: "Read the event log."},

	{Action: "audit:read", IsWrite: false,
		Routes:      []Route{{"GET", "/v1/audit"}},
		MCPTools:    []string{"audit_list"},
		Description: "Read the auth audit log."},

	{Action: "lineage:read", IsWrite: false,
		Routes: []Route{
			{"GET", "/v1/lineage/runs/{run_id}"},
			{"GET", "/v1/lineage/runs/{run_id}/ancestors"},
			{"GET", "/v1/lineage/runs/{run_id}/descendants"},
			{"GET", "/v1/lineage/claims/{claim_handle_id}"},
			{"GET", "/v1/lineage/claims/{claim_handle_id}/ancestors"},
			{"GET", "/v1/lineage/by-source/{source_type}/{source_id}"},
			{"GET", "/v1/lineage/by-producer/{executor_name}"},
		},
		MCPTools:    []string{"lineage_get"},
		Description: "Read lineage graphs."},
	{Action: "lineage:prune", IsWrite: true,
		Routes:      []Route{{"POST", "/v1/admin/lineage/prune"}},
		MCPTools:    []string{"lineage_prune"},
		Description: "Prune lineage rows older than a cutoff."},

	{Action: "parked-node:read", IsWrite: false,
		Routes: []Route{
			{"GET", "/v1/diagnostics/parked"},
			{"GET", "/v1/admin/diagnostics/parked-nodes"},
		},
		MCPTools:    []string{"parked_node_list"},
		Description: "List nodes parked in the wait-set."},

	{Action: "waitset:read", IsWrite: false,
		Routes:      []Route{{"GET", "/v1/admin/diagnostics/wait-sets"}},
		MCPTools:    []string{"waitset_list"},
		Description: "List wait-set entries (sender/receiver edges)."},

	{Action: "claim-holders:read", IsWrite: false,
		Routes:      []Route{{"GET", "/v1/lock-holders/{claim_handle_id}/claim-holders"}},
		MCPTools:    []string{"claim_holders_list"},
		Description: "List claim-holder rows for a claim handle."},

	{Action: "asset:read", IsWrite: false,
		Routes: []Route{
			{"GET", "/v1/instances/{id}/assets"},
			{"GET", "/v1/instances/{id}/assets/{alias}"},
			{"GET", "/v1/instances/{id}/assets/{alias}/versions"},
			{"GET", "/v1/instances/{id}/assets/{alias}/materialization-history"},
		},
		MCPTools:    []string{"asset_list", "asset_get", "asset_versions", "asset_materialization_history"},
		Description: "Read assets on an instance."},
	{Action: "asset:delete", IsWrite: true,
		Routes:      []Route{{"DELETE", "/v1/instances/{id}/assets/{alias}"}},
		MCPTools:    []string{"asset_delete"},
		Description: "Delete an asset on an instance."},

	{Action: "diagnostics:read", IsWrite: false,
		Routes:      []Route{{"GET", "/v1/admin/diagnostics/held-frames"}},
		MCPTools:    []string{"held_frames_list"},
		Description: "List frames held by holding-subgraph claims."},

	{Action: "auth:read", IsWrite: false,
		Routes: []Route{
			{"GET", "/v1/auth/keys"},
			{"GET", "/v1/auth/keys/{nameOrID}"},
			{"GET", "/v1/auth/status"},
		},
		MCPTools:    []string{"auth_list", "auth_get", "auth_status"},
		Description: "Read API-key state and the auth mode (anonymous|authenticated)."},
	{Action: "auth:create", IsWrite: true,
		Routes:      []Route{{"POST", "/v1/auth/keys"}},
		MCPTools:    []string{"auth_create_key"},
		Description: "Mint a new API key. Plaintext surfaced exactly once."},
	{Action: "auth:revoke", IsWrite: true,
		Routes:      []Route{{"DELETE", "/v1/auth/keys/{nameOrID}"}},
		MCPTools:    []string{"auth_revoke_key"},
		Description: "Revoke an API key. Refuses to leave zero active keys without force flag."},
	{Action: "auth:rotate", IsWrite: true,
		Routes:      []Route{{"POST", "/v1/auth/keys/{nameOrID}/rotate"}},
		MCPTools:    []string{"auth_rotate_key"},
		Description: "Rotate an API key (mint a new plaintext with same identity; old key revoked at grace expiry)."},

	{Action: "observability:read", IsWrite: false,
		Routes:      []Route{{"GET", "/v1/observability/*"}},
		MCPTools:    nil,
		Description: "Read observability data via /v1/observability/*."},

	// @concept: permission
	{Action: composeOriginAction, IsWrite: false,
		Routes:      nil,
		MCPTools:    nil,
		Description: "Capability marker for the privileged compose-CLI: grants the bearer the right to create reserved-prefix `compose:<project>:<...>` tags and instance keys. No route maps to this action; it is consulted server-side when a request stamps the X-Rimsky-Compose-Origin header."},

	{Action: "mcp:read", IsWrite: false,
		Routes:      []Route{{"POST", "/v1/mcp"}, {"GET", "/v1/mcp"}},
		MCPTools:    nil,
		Description: "Invoke the MCP JSON-RPC dispatch surface (POST) and open the server-to-client stream (GET); per-tool actions still gate tools/call."},
}
