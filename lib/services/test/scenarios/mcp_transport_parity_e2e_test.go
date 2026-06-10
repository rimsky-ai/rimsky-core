// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// End-to-end proof that the MCP JSON-RPC transport at POST /v1/mcp delivers
// the same surface — auth gating, response shape, observable state — as the
// equivalent HTTP routes against the REAL assembled product.
//
// STORY-mcp-transport: an operator (or AI agent) using rimsky through an
// MCP client can perform every read and mutation through the MCP tool
// surface that the HTTP surface offers, with the same auth and permission
// semantics. The MCP skin must NOT be a parallel implementation — every
// tool dispatches back through the chi router, the auth middleware re-runs
// per-tool, and the underlying handler does the real work. A canned MCP
// response or a weaker MCP-side gate would falsify the parity claim.
//
// This test boots the assembled all-in-one stack (control-api + scheduler
// + supervisor on baked SQLite) plus the in-tree stub executor, stands up
// an in-test MCP client (the JSON-RPC envelope + tools/call dispatch — no
// shared MCP client package exists in-tree), and drives parity across the
// thirteen tool categories the story names: template, tag, instance, node,
// message, event, audit, breakpoint, asset, backfill, lineage,
// diagnostics, auth.
//
// Parity is proven on two axes per category:
//
//  1. AUTH PARITY — a key minted with ONLY `mcp:read` (sufficient to reach
//     POST /v1/mcp and run tools/list, but holding NO per-category
//     permissions) attempts a representative tool per category and observes
//     an `isError: true` envelope with HTTP status 403. The same key
//     calling the SAME HTTP route directly also returns 403. Identical
//     deny semantics.
//
//  2. OBSERVABLE-STATE PARITY — the admin key (Grant `*`) invokes a read
//     tool per category and the response shape matches what the HTTP route
//     returns; the admin key then invokes a mutation tool per writable
//     category and the side effect is visible via the corresponding HTTP
//     read (proving the handler ran, not a canned response).
//
// Categories where mutation is not naturally bounded (asset_materialize
// requires a real upstream data-processing-capable producer; backfill_create
// requires a fan-out node; lineage_prune is destructive) are sampled on the
// read axis only. Auth-parity is asserted for every category — that is the
// load-bearing falsifier.
//
// Falsifier: An MCP tool gate is weaker than the equivalent HTTP route's
// gate (bypasses auth), OR an MCP tool returns a canned response without
// invoking the real handler. The first is caught by asserting deny-shape
// parity per category; the second is caught by asserting the mutation
// changes observable state visible through the HTTP route.
package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/services/test/harness"
)

// mcpTransportToolCategory describes one of the thirteen surfaces the
// story names. Each carries: a representative read-tool name (always
// asserted on both auth and observable-state parity axes), an optional
// mutation-tool name + args + HTTP-route mirror (asserted on auth parity
// always; on observable-state parity when mutationVerifier is non-nil).
//
// The HTTP-route mirror is the canonical route the auth-gate-deny test
// hits with the same low-permission bearer to confirm the gate denies
// identically on both transports. This is the cross-transport assertion
// — auth deny on MCP must reflect auth deny on HTTP, not a separate
// weaker check.
type mcpTransportToolCategory struct {
	name string // human-readable label used in test failure messages

	// Read tool (always exercised).
	readTool     string
	readArgs     map[string]any
	readHTTPVerb string
	readHTTPPath string

	// Mutation tool (optional). Empty mutationTool means the category
	// is sampled on the read axis only — for categories whose mutations
	// either are not naturally bounded (asset_materialize) or are
	// already covered by another category's mutation flow.
	mutationTool     string
	mutationArgs     map[string]any
	mutationHTTPVerb string
	mutationHTTPPath string
}

// TestMcpTransportParity drives parity between POST /v1/mcp and the
// equivalent HTTP routes across the thirteen tool categories the story
// names. Asserts auth-gate parity for every category and observable-state
// parity for representative mutations.
func TestMcpTransportParity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// The stub executor must be reachable on the shared network before
	// rimsky/all starts — the control-api fires a Capabilities handshake
	// against declared executors at startup. Network first, then the
	// executor peer, then rimsky on the baked SQLite default.
	netName := harness.NewNetwork(ctx, t)
	harness.StartExecutorStubOnNetwork(ctx, t, netName, "executor-stub")

	ep := harness.BringUpRimsky(ctx, t,
		harness.WithSQLite(),
		harness.WithExistingNetwork(netName),
		harness.WithExecutor("stub", "executor-stub:9300"),
	)

	// ---- Seed identities. ----
	//
	// Anonymous mode is open while no api-keys exist. Minting the first
	// key bootstraps the admin (Grant `*`) and closes anon mode; from
	// here every request needs a Bearer. The second mint produces a
	// minimal key holding ONLY `mcp:read` — enough to reach /v1/mcp
	// (initialize + tools/list + tools/call dispatch) but nothing else.
	// That key is the one we drive the auth-parity assertions with.
	adminKey := mintAPIKey(t, ep, "", "mcp-parity-admin", []map[string]any{
		{"action": "*"},
	})
	mcpOnlyKey := mintAPIKey(t, ep, adminKey, "mcp-parity-mcponly", []map[string]any{
		{"action": "mcp:read"},
	})

	// ---- Seed state for the parity assertions. ----
	//
	// A registered + deployed template + a live instance gives every
	// category a real entity to read / mutate against. The same shape
	// the other SQLite e2e tests use — one stub-backed worker node so
	// the supervisor settles it through the real dispatch path.
	templateID := deploySQLiteTemplateAuth(t, ep, adminKey, map[string]any{
		"spec": map[string]any{
			"name":                  "mcp-transport-parity",
			"version":               "1",
			"frame_resolution_mode": "serial_queue",
			"frame_timeout_ms":      600000,
			"nodes": []map[string]any{
				{"type": "worker", "executor": "stub"},
			},
		},
	})

	// Create an instance via HTTP so we have a known instance to read /
	// mutate against from MCP. The instance is durable (no
	// terminate_after_run) so node-list / breakpoint-create / message-
	// send have a non-terminal instance to operate against throughout the
	// test.
	instanceKey := "mcp-parity-seed"
	instanceID := createInstanceAuth(t, ep, adminKey, templateID, instanceKey, map[string]any{})

	// Wait for the worker node to be created by the scheduler so that
	// node-category reads have something to surface. The supervisor
	// dispatches the node against the stub which returns Success; the
	// node settles to `fresh` quickly. We need it created (not terminal)
	// for node_invalidate's downstream effect to be observable — but since
	// invalidate works equally on a settled fresh node (re-fire), waiting
	// for `fresh` is also fine.
	nodeID := waitForFirstNodeID(t, ep, adminKey, instanceID, 60*time.Second)

	// ---- MCP client handshake ---- per spec.MCP-as-skin: initialize
	// must mint a session id; notifications/initialized must be 202 with
	// empty body (a JSON-RPC notification, NEVER a JSON-RPC error reply);
	// tools/list returns the catalog filtered by the requesting identity.
	mcpURL := ep.BaseURL + "/v1/mcp"

	// Sanity: initialize succeeds and issues a session id (admin Bearer).
	sessionID := mcpInitialize(t, mcpURL, adminKey)
	mcpNotifyInitialized(t, mcpURL, adminKey, sessionID)

	// tools/list (admin) must enumerate at least one tool per category
	// the story names. The catalog is filtered by the requesting identity
	// — admin holds `*` so it sees everything; mcpOnlyKey holds only
	// `mcp:read` so its catalog should be empty.
	adminTools := mcpToolsList(t, mcpURL, adminKey, sessionID)
	assertToolCatalogCoverage(t, adminTools)

	// The mcpOnlyKey holds only `mcp:read` — Filtered() walks every tool
	// and includes only those whose action the identity's grant matches;
	// `mcp:read` matches no other action, so the catalog is empty. This is
	// the catalog-side mirror of the per-tool deny: the identity sees
	// nothing it cannot use.
	mcpOnlySessionID := mcpInitialize(t, mcpURL, mcpOnlyKey)
	mcpNotifyInitialized(t, mcpURL, mcpOnlyKey, mcpOnlySessionID)
	mcpOnlyTools := mcpToolsList(t, mcpURL, mcpOnlyKey, mcpOnlySessionID)
	if len(mcpOnlyTools) != 0 {
		t.Fatalf("mcp-only key (grant {mcp:read}) saw %d tools; want 0 (Filtered() must exclude every tool whose action the grant does not match)\ntools: %v",
			len(mcpOnlyTools), mcpOnlyTools)
	}

	// ---- Per-category parity assertions. ----
	//
	// Build the catalog of representative read + (optionally) mutation
	// tools per category. Each entry's args reference the seed state
	// (templateID, instanceID, nodeID). Mutation args are chosen so the
	// observable side effect (a created entity, a state change) is
	// visible through the HTTP route mirror without setting up complex
	// preconditions.
	categories := []mcpTransportToolCategory{
		// Template: read = list; mutation = register a fresh template
		// (visible via /v1/templates/{id} read).
		{
			name:         "template",
			readTool:     "template_list",
			readHTTPVerb: http.MethodGet,
			readHTTPPath: "/v1/templates",
			mutationTool: "template_register",
			mutationArgs: map[string]any{
				"spec": map[string]any{
					"name":                  "mcp-transport-parity-mut",
					"version":               "1",
					"frame_resolution_mode": "serial_queue",
					"nodes": []map[string]any{
						{"type": "worker", "executor": "stub"},
					},
				},
			},
			mutationHTTPVerb: http.MethodPost,
			mutationHTTPPath: "/v1/templates",
		},
		// Tag: read = list; mutation = create a tag pointing at the
		// seed templateID (visible via /v1/tags).
		{
			name:         "tag",
			readTool:     "tag_list",
			readHTTPVerb: http.MethodGet,
			readHTTPPath: "/v1/tags",
			mutationTool: "tag_create",
			mutationArgs: map[string]any{
				"tag":      "mcp-parity-tag",
				"template": templateID,
			},
			mutationHTTPVerb: http.MethodPost,
			mutationHTTPPath: "/v1/tags",
		},
		// Instance: read = list; mutation = create a fresh instance
		// (visible via /v1/instances/{key}).
		{
			name:         "instance",
			readTool:     "instance_list",
			readHTTPVerb: http.MethodGet,
			readHTTPPath: "/v1/instances",
			mutationTool: "instance_create",
			mutationArgs: map[string]any{
				"template":     templateID,
				"instance_key": "mcp-parity-mut-inst",
				"params":       map[string]any{},
			},
			mutationHTTPVerb: http.MethodPost,
			mutationHTTPPath: "/v1/instances",
		},
		// Node: read = list (by instance); mutation = invalidate the
		// seed node (observable via the operator_override audit event).
		{
			name:     "node",
			readTool: "node_list",
			readArgs: map[string]any{
				"idOrKey": instanceID,
			},
			readHTTPVerb: http.MethodGet,
			readHTTPPath: "/v1/instances/" + instanceID + "/nodes",
			mutationTool: "node_invalidate",
			mutationArgs: map[string]any{
				"id":     nodeID,
				"reason": "mcp parity test",
			},
			mutationHTTPVerb: http.MethodPost,
			mutationHTTPPath: "/v1/nodes/" + nodeID + "/invalidate",
		},
		// Message: read = list (by instance); mutation = send a real
		// invalidate message (the only V1 kind) into the seed instance.
		// Observable via /v1/instances/{id}/messages.
		{
			name:     "message",
			readTool: "message_list",
			readArgs: map[string]any{
				"id": instanceID,
			},
			readHTTPVerb: http.MethodGet,
			readHTTPPath: "/v1/instances/" + instanceID + "/messages",
			mutationTool: "message_send",
			mutationArgs: map[string]any{
				"id":   instanceID,
				"kind": "invalidate",
			},
			mutationHTTPVerb: http.MethodPost,
			mutationHTTPPath: "/v1/instances/" + instanceID + "/messages",
		},
		// Event: read-only category. read = list.
		{
			name:         "event",
			readTool:     "event_list",
			readHTTPVerb: http.MethodGet,
			readHTTPPath: "/v1/events",
		},
		// Audit: read-only category. read = list (gated by `audit:read`).
		{
			name:         "audit",
			readTool:     "audit_list",
			readHTTPVerb: http.MethodGet,
			readHTTPPath: "/v1/audit",
		},
		// Breakpoint: read = list (by instance); mutation = create a
		// breakpoint on the worker node's after_terminal checkpoint
		// (visible via /v1/instances/{idOrKey}/breakpoints).
		{
			name:     "breakpoint",
			readTool: "breakpoint_list",
			readArgs: map[string]any{
				"idOrKey": instanceID,
			},
			readHTTPVerb: http.MethodGet,
			readHTTPPath: "/v1/instances/" + instanceID + "/breakpoints",
			mutationTool: "breakpoint_create",
			mutationArgs: map[string]any{
				"idOrKey":     instanceID,
				"checkpoint":  "after_terminal",
				"signal_type": "terminal/success",
				"mode":        "notify_only",
			},
			mutationHTTPVerb: http.MethodPost,
			mutationHTTPPath: "/v1/instances/" + instanceID + "/breakpoints",
		},
		// Asset: read-only sampling. The seed template doesn't declare a
		// data-processing-capable producer so asset_materialize would
		// need additional setup; the auth-parity assertion below still
		// fires against the read route, covering the load-bearing
		// falsifier (an MCP-side gate weaker than the HTTP-side gate).
		{
			name:     "asset",
			readTool: "asset_list",
			readArgs: map[string]any{
				"id": instanceID,
			},
			readHTTPVerb: http.MethodGet,
			readHTTPPath: "/v1/instances/" + instanceID + "/assets",
		},
		// Backfill: read-only sampling. backfill_create requires a
		// target_node that the seed template would also need to declare
		// as fan-out-capable; the read surface still exhibits parity.
		{
			name:     "backfill",
			readTool: "backfill_list",
			readArgs: map[string]any{
				"id": instanceID,
			},
			readHTTPVerb: http.MethodGet,
			readHTTPPath: "/v1/instances/" + instanceID + "/backfills",
		},
		// Lineage: read-only sampling — the surface returns empty for an
		// instance that hasn't produced lineage records yet but the
		// auth-parity test fires identically on emptiness.
		{
			name:     "lineage",
			readTool: "lineage_get",
			readArgs: map[string]any{
				"executor_name": "stub",
			},
			readHTTPVerb: http.MethodGet,
			readHTTPPath: "/v1/lineage/by-producer/stub",
		},
		// Diagnostics: read-only category. parked_node_list gates
		// against `parked-node:read`. The route returns an empty list
		// when nothing is parked but the gate still fires identically.
		{
			name:         "diagnostics",
			readTool:     "parked_node_list",
			readHTTPVerb: http.MethodGet,
			readHTTPPath: "/v1/diagnostics/parked",
		},
		// Auth: read = list keys (gated by `auth:read`); mutation =
		// mint a fresh key (visible via /v1/auth/keys read).
		{
			name:         "auth",
			readTool:     "auth_list",
			readHTTPVerb: http.MethodGet,
			readHTTPPath: "/v1/auth/keys",
			mutationTool: "auth_create_key",
			mutationArgs: map[string]any{
				"name":        "mcp-parity-mut-key",
				"permissions": []map[string]any{{"action": "instance:read"}},
			},
			mutationHTTPVerb: http.MethodPost,
			mutationHTTPPath: "/v1/auth/keys",
		},
	}

	// ---- Axis 1: AUTH PARITY ---- For each category, drive the
	// representative read tool (and mutation tool, if any) with the
	// mcpOnlyKey and assert the MCP envelope is an isError-403 result.
	// Compare against the same HTTP route hit with the same bearer:
	// must also be 403. Identical deny shape across both transports.
	for _, cat := range categories {
		cat := cat
		t.Run("auth_parity/"+cat.name, func(t *testing.T) {
			t.Parallel()
			assertAuthParity(t, ep, mcpURL, mcpOnlyKey, mcpOnlySessionID, cat.readTool, cat.readArgs, cat.readHTTPVerb, cat.readHTTPPath)
			if cat.mutationTool != "" {
				assertAuthParity(t, ep, mcpURL, mcpOnlyKey, mcpOnlySessionID, cat.mutationTool, cat.mutationArgs, cat.mutationHTTPVerb, cat.mutationHTTPPath)
			}
		})
	}

	// ---- Axis 2: OBSERVABLE-STATE PARITY ---- Admin-key reads return
	// real entities; admin-key mutations cause real state changes
	// visible to subsequent HTTP-route reads. A canned MCP response
	// (the second falsifier) would either return a shape that doesn't
	// match the HTTP route's shape (read parity), or leave no state
	// change visible afterwards (mutation parity).
	//
	// We assert these sequentially (not in parallel) so the seed state
	// stays predictable — a mutation in one category can show up in
	// subsequent categories' reads (a created template appears in
	// /v1/templates, a created instance in /v1/instances, etc.) and
	// that's part of the parity story.
	for _, cat := range categories {
		assertObservableStateReadParity(t, ep, mcpURL, adminKey, sessionID, cat)
		if cat.mutationTool != "" {
			assertObservableStateMutationParity(t, ep, mcpURL, adminKey, sessionID, cat)
		}
	}
}

// assertToolCatalogCoverage verifies tools/list returns a representative
// tool for each of the thirteen categories the story names — the
// "discovers a tool catalog covering ..." acceptance leg. A category
// missing from the catalog falsifies the parity claim: the operator's
// agent has no way to drive that surface through MCP.
func assertToolCatalogCoverage(t *testing.T, tools []toolEntry) {
	t.Helper()
	have := map[string]bool{}
	for _, tool := range tools {
		have[tool.Name] = true
	}
	// One representative read tool per category — the same tool name
	// each category's parity assertion drives below.
	must := []string{
		"template_list",
		"tag_list",
		"instance_list",
		"node_list",
		"message_list",
		"event_list",
		"audit_list",
		"breakpoint_list",
		"asset_list",
		"backfill_list",
		"lineage_get",
		"parked_node_list",
		"auth_list",
	}
	for _, name := range must {
		if !have[name] {
			t.Fatalf("admin tools/list missing %q — the category's surface is unreachable via MCP\nhave: %v", name, toolNames(tools))
		}
	}
}

// assertAuthParity drives one MCP tools/call with the low-permission
// bearer + the same HTTP-route request with the same bearer, and asserts
// both deny with HTTP 403. Asymmetry — MCP allowing where HTTP denies, or
// vice versa — falsifies the parity claim.
func assertAuthParity(t *testing.T, ep harness.RimskyEndpoint, mcpURL, bearer, sessionID, toolName string, args map[string]any, httpVerb, httpPath string) {
	t.Helper()
	// HTTP route side: a raw request from the same bearer must return
	// 403. The HTTP gate is the canonical reference; MCP must mirror it.
	httpStatus := requestStatusAuth(t, ep, httpVerb, httpPath, bearer, nil)
	if httpStatus != http.StatusForbidden {
		t.Fatalf("HTTP %s %s with bearer holding only mcp:read returned %d, want 403 — the HTTP-route gate must deny", httpVerb, httpPath, httpStatus)
	}

	// MCP side: same bearer, tools/call invocation; the envelope must
	// surface isError:true with status 403. The MCP skin re-enters the
	// chi router, so the per-tool action gate runs against the same
	// auth middleware — anything else (allowed, different status,
	// canned response) means the MCP transport diverged from HTTP.
	envelope := mcpToolsCall(t, mcpURL, bearer, sessionID, toolName, args)
	status, isErr := unwrapMcpErrorEnvelope(t, envelope, toolName)
	if !isErr {
		t.Fatalf("MCP tools/call %q with bearer holding only mcp:read returned a success envelope — MCP-side gate is weaker than HTTP route's gate\nenvelope: %v", toolName, envelope)
	}
	if status != http.StatusForbidden {
		t.Fatalf("MCP tools/call %q with bearer holding only mcp:read returned status %d, want 403 — MCP must mirror the HTTP-route 403\nenvelope: %v", toolName, status, envelope)
	}
}

// assertObservableStateReadParity asserts that an MCP read tool driven
// by the admin key returns a JSON shape congruent with the same HTTP
// route's response, proving the MCP layer dispatches to the real
// handler rather than returning a canned shape.
//
// Congruence is asserted by parsing both responses as JSON and matching
// top-level keys: the route's body has a stable set of top-level keys
// (e.g. /v1/templates returns {"templates": [...], "next_cursor": ...};
// /v1/instances returns {"instances": ...}; /v1/auth/keys returns
// {"keys": ...}). The MCP envelope unwraps to the same body the route
// returned, so the same top-level keys must appear. A canned MCP shape
// would have different keys; a real-handler dispatch produces the
// route's keys.
func assertObservableStateReadParity(t *testing.T, ep harness.RimskyEndpoint, mcpURL, bearer, sessionID string, cat mcpTransportToolCategory) {
	t.Helper()

	// HTTP body — canonical reference shape.
	status, raw := getJSONAuth(t, ep, cat.readHTTPPath, bearer)
	if status != http.StatusOK {
		t.Fatalf("%s read parity: HTTP %s returned %d, want 200\nbody: %s", cat.name, cat.readHTTPPath, status, string(raw))
	}
	var httpBody map[string]json.RawMessage
	if err := json.Unmarshal(raw, &httpBody); err != nil {
		t.Fatalf("%s read parity: decode HTTP %s body: %v\nraw: %s", cat.name, cat.readHTTPPath, err, string(raw))
	}

	// MCP body — must unwrap to the same shape.
	envelope := mcpToolsCall(t, mcpURL, bearer, sessionID, cat.readTool, cat.readArgs)
	mcpBody := unwrapMcpSuccessEnvelope(t, envelope, cat.readTool)
	var mcpMap map[string]json.RawMessage
	switch v := mcpBody.(type) {
	case map[string]any:
		mcpMap = map[string]json.RawMessage{}
		for k, val := range v {
			b, err := json.Marshal(val)
			if err != nil {
				t.Fatalf("%s read parity: re-marshal MCP body key %q: %v", cat.name, k, err)
			}
			mcpMap[k] = b
		}
	default:
		t.Fatalf("%s read parity: MCP %q response is not a JSON object (canned response shape?); got %T\nenvelope: %v", cat.name, cat.readTool, mcpBody, envelope)
	}

	// Every top-level key the HTTP route returned must appear in the
	// MCP body. (Reverse: MCP may carry the same keys plus nothing else —
	// the catalog forwards the route's bytes verbatim.) If a route key
	// is missing from MCP, the MCP transport returned a canned shape.
	for k := range httpBody {
		if _, ok := mcpMap[k]; !ok {
			t.Fatalf("%s read parity: MCP %q response missing top-level key %q present on HTTP %s response — MCP transport returned a canned shape\nhttp keys: %v\nmcp keys: %v",
				cat.name, cat.readTool, k, cat.readHTTPPath, jsonMapKeys(httpBody), jsonMapKeys(mcpMap))
		}
	}
}

// assertObservableStateMutationParity asserts that an MCP mutation tool
// driven by the admin key causes a state change visible through the
// equivalent HTTP read route, proving the MCP layer dispatches to the
// real write handler rather than returning a canned success envelope.
func assertObservableStateMutationParity(t *testing.T, ep harness.RimskyEndpoint, mcpURL, bearer, sessionID string, cat mcpTransportToolCategory) {
	t.Helper()

	envelope := mcpToolsCall(t, mcpURL, bearer, sessionID, cat.mutationTool, cat.mutationArgs)
	// Mutation parity requires the MCP write to actually succeed — if the
	// catalog surfaced an error envelope here, the mutation never landed
	// and any subsequent observable-state check would mis-attribute the
	// absence to a canned-response defect rather than the real cause
	// (a bad arg, a stale route, etc.). Detect the surface-as-error
	// envelope up front so failures point at the actual problem.
	if status, isErr := unwrapMcpErrorEnvelopeOptional(envelope, cat.mutationTool); isErr {
		t.Fatalf("%s mutation parity: MCP %q returned an error envelope (status=%d); the mutation never landed so observable-state parity is moot\nenvelope: %v",
			cat.name, cat.mutationTool, status, envelope)
	}
	body := unwrapMcpSuccessEnvelope(t, envelope, cat.mutationTool)

	// Each mutation has a specific observable consequence on the HTTP
	// surface. Verify the relevant downstream read returns the new state.
	switch cat.name {
	case "template":
		// /v1/templates returned a template_id; the same id must be
		// readable via /v1/templates/{id} (template:read).
		m, ok := body.(map[string]any)
		if !ok {
			t.Fatalf("template mutation parity: response not a JSON object: %T\nbody: %v", body, body)
		}
		newID, _ := m["template_id"].(string)
		if newID == "" {
			t.Fatalf("template mutation parity: template_register response missing template_id\nbody: %v", m)
		}
		status, raw := getJSONAuth(t, ep, "/v1/templates/"+newID, bearer)
		if status != http.StatusOK {
			t.Fatalf("template mutation parity: GET /v1/templates/%s returned %d, want 200 (the template MCP claimed to create must be readable via HTTP)\nbody: %s", newID, status, string(raw))
		}

	case "tag":
		// /v1/tags must now list the created tag.
		if !tagListedAuth(t, ep, adminBearer(t, bearer), "mcp-parity-tag") {
			t.Fatalf("tag mutation parity: tag %q is not on /v1/tags after MCP tag_create — MCP write returned canned success without invoking the handler", "mcp-parity-tag")
		}

	case "instance":
		// /v1/instances/{key} must now return 200.
		key := "mcp-parity-mut-inst"
		status, raw := getJSONAuth(t, ep, "/v1/instances/"+key, bearer)
		if status != http.StatusOK {
			t.Fatalf("instance mutation parity: GET /v1/instances/%s returned %d, want 200 (the instance MCP claimed to create must be readable via HTTP)\nbody: %s", key, status, string(raw))
		}

	case "node":
		// node_invalidate emits an operator_override audit event. The
		// audit route surfaces it; the simplest check is that the
		// invalidate route's response body returns a 200 (the catalog
		// surfaces non-error responses as a JSON body). The downstream
		// effect — that the supervisor re-fires the node — is asserted
		// by checking the unified event log for a recent
		// operator_override entry.
		assertOperatorOverrideEvent(t, ep, adminBearer(t, bearer), instanceFromNode(t, ep, bearer, cat.mutationArgs["id"].(string)), 30*time.Second)

	case "message":
		// /v1/instances/{id}/messages must now include at least one
		// invalidate message.
		instID := cat.mutationArgs["id"].(string)
		status, raw := getJSONAuth(t, ep, "/v1/instances/"+instID+"/messages", bearer)
		if status != http.StatusOK {
			t.Fatalf("message mutation parity: GET /v1/instances/%s/messages returned %d, want 200\nbody: %s", instID, status, string(raw))
		}
		if !strings.Contains(string(raw), `"kind":"invalidate"`) {
			t.Fatalf("message mutation parity: GET /v1/instances/%s/messages does not include an invalidate message after MCP message_send\nbody: %s", instID, string(raw))
		}

	case "breakpoint":
		// /v1/instances/{idOrKey}/breakpoints must list the new
		// breakpoint. The supervisor materializes the row inside the
		// create transaction so a fresh list should see it; small poll
		// guards any projection lag.
		idOrKey := cat.mutationArgs["idOrKey"].(string)
		assertBreakpointAppears(t, ep, bearer, idOrKey, "after_terminal", 10*time.Second)

	case "auth":
		// /v1/auth/keys must now list the new key by name.
		status, raw := getJSONAuth(t, ep, "/v1/auth/keys", bearer)
		if status != http.StatusOK {
			t.Fatalf("auth mutation parity: GET /v1/auth/keys returned %d, want 200\nbody: %s", status, string(raw))
		}
		if !strings.Contains(string(raw), `"mcp-parity-mut-key"`) {
			t.Fatalf("auth mutation parity: GET /v1/auth/keys does not include the key MCP claimed to mint\nbody: %s", string(raw))
		}

	default:
		t.Fatalf("mutation parity case missing for category %q — add it or set mutationTool to empty", cat.name)
	}

	// All MCP success envelopes must carry a non-nil body — a canned
	// blank envelope would slip past the category-specific assertion if
	// the route happened to return an empty object too.
	if body == nil {
		t.Fatalf("%s mutation parity: MCP %q returned a nil body (canned blank envelope?)", cat.name, cat.mutationTool)
	}
}

// ---------- MCP wire helpers (in-test minimal JSON-RPC client). ----------

// toolEntry mirrors mcp.Tool just enough to decode tools/list output.
type toolEntry struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// mcpInitialize POSTs the initialize JSON-RPC message, asserts a
// Mcp-Session-Id is issued, and returns it. The bearer is forwarded as
// Authorization so the `mcp:read` umbrella gate accepts the request.
func mcpInitialize(t *testing.T, mcpURL, bearer string) string {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize"}`
	resp := postMCPJSONRPC(t, mcpURL, bearer, "", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("mcp initialize: got %d, want 200\nbody: %s", resp.StatusCode, string(raw))
	}
	sid := resp.Header.Get("Mcp-Session-Id")
	if sid == "" {
		t.Fatalf("mcp initialize: missing Mcp-Session-Id header")
	}
	return sid
}

// mcpNotifyInitialized POSTs the notifications/initialized notification
// and asserts the server returns 202/empty per JSON-RPC 2.0 — a
// notification gets no JSON-RPC reply. A JSON-RPC error envelope here
// would be a JSON-RPC 2.0 violation.
func mcpNotifyInitialized(t *testing.T, mcpURL, bearer, sessionID string) {
	t.Helper()
	body := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	resp := postMCPJSONRPC(t, mcpURL, bearer, sessionID, body)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("mcp notifications/initialized: got %d, want 202\nbody: %s", resp.StatusCode, string(raw))
	}
	if strings.TrimSpace(string(raw)) != "" {
		t.Fatalf("mcp notifications/initialized: must return empty body; got %q", string(raw))
	}
}

// mcpToolsList POSTs tools/list and returns the decoded tools array.
// The result envelope is `{"tools": [...]}` per the MCP spec.
func mcpToolsList(t *testing.T, mcpURL, bearer, sessionID string) []toolEntry {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
	resp := postMCPJSONRPC(t, mcpURL, bearer, sessionID, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("mcp tools/list: got %d, want 200\nbody: %s", resp.StatusCode, string(raw))
	}
	var env struct {
		Result struct {
			Tools []toolEntry `json:"tools"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("mcp tools/list decode: %v", err)
	}
	if env.Error != nil {
		t.Fatalf("mcp tools/list returned error: %+v", env.Error)
	}
	return env.Result.Tools
}

// mcpToolsCallEnvelope is the decoded MCP tools/call result envelope:
// `{"content": [{"type":"text","text": "<JSON of inner body>"}]}`. The
// inner body's text — JSON-encoded — is the response the underlying
// HTTP handler returned, or `{"status": 4xx, "error": true, "body":
// ..., "isError": true}` for an error.
type mcpToolsCallEnvelope struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// mcpToolsCall POSTs tools/call with name + arguments and returns the
// decoded result envelope. A JSON-RPC error reply (e.g. method not
// found) fails the test — those are wire-level errors, distinct from
// tool-result errors (which surface as isError:true within the success
// envelope's content).
func mcpToolsCall(t *testing.T, mcpURL, bearer, sessionID, name string, args map[string]any) mcpToolsCallEnvelope {
	t.Helper()
	params := map[string]any{"name": name, "arguments": args}
	pb, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("mcp tools/call %q: marshal params: %v", name, err)
	}
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":%s}`, string(pb))
	resp := postMCPJSONRPC(t, mcpURL, bearer, sessionID, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("mcp tools/call %q: got %d, want 200\nbody: %s", name, resp.StatusCode, string(raw))
	}
	var env struct {
		Result mcpToolsCallEnvelope `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("mcp tools/call %q: decode: %v", name, err)
	}
	if env.Error != nil {
		t.Fatalf("mcp tools/call %q returned JSON-RPC error: %+v", name, env.Error)
	}
	return env.Result
}

// postMCPJSONRPC POSTs one JSON-RPC body to the /v1/mcp endpoint with
// the Authorization + Mcp-Session-Id headers when supplied. The caller
// is responsible for closing the returned response body.
func postMCPJSONRPC(t *testing.T, mcpURL, bearer, sessionID, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, mcpURL, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build MCP POST: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("MCP POST: %v", err)
	}
	return resp
}

// unwrapMcpSuccessEnvelope decodes a tools/call success envelope and
// returns the JSON-decoded inner body. The MCP transport wraps the
// underlying handler's response as `content[0].text` (a JSON string);
// the inner text JSON-decodes to whatever shape the HTTP route returned
// — typically a map (`{"templates": [...]}`, etc.) or a list.
func unwrapMcpSuccessEnvelope(t *testing.T, env mcpToolsCallEnvelope, toolName string) any {
	t.Helper()
	if len(env.Content) == 0 {
		t.Fatalf("MCP tools/call %q: envelope has no content entries", toolName)
	}
	c := env.Content[0]
	if c.Type != "text" {
		t.Fatalf("MCP tools/call %q: content[0].type=%q, want text", toolName, c.Type)
	}
	var body any
	if c.Text == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(c.Text), &body); err != nil {
		t.Fatalf("MCP tools/call %q: decode inner body: %v\nraw: %s", toolName, err, c.Text)
	}
	return body
}

// unwrapMcpErrorEnvelope decodes a tools/call error envelope: the inner
// body is `{"status": N, "error": true, "body": ..., "isError": true}`
// per the catalog's surface-as-error convention. Returns the HTTP-side
// status and whether the body actually carries the isError marker.
func unwrapMcpErrorEnvelope(t *testing.T, env mcpToolsCallEnvelope, toolName string) (int, bool) {
	t.Helper()
	body := unwrapMcpSuccessEnvelope(t, env, toolName)
	m, ok := body.(map[string]any)
	if !ok {
		return 0, false
	}
	isErr, _ := m["isError"].(bool)
	statusF, _ := m["status"].(float64)
	return int(statusF), isErr
}

// unwrapMcpErrorEnvelopeOptional is the t-free variant the mutation
// parity assertion uses to detect a surface-as-error envelope without
// failing the test — the caller decides whether the error is expected
// or a regression. Returns (status, isError); isError=false means the
// envelope is a real success, not an error.
func unwrapMcpErrorEnvelopeOptional(env mcpToolsCallEnvelope, toolName string) (int, bool) {
	if len(env.Content) == 0 {
		return 0, false
	}
	c := env.Content[0]
	if c.Type != "text" || c.Text == "" {
		return 0, false
	}
	var body any
	if err := json.Unmarshal([]byte(c.Text), &body); err != nil {
		return 0, false
	}
	m, ok := body.(map[string]any)
	if !ok {
		return 0, false
	}
	isErr, _ := m["isError"].(bool)
	statusF, _ := m["status"].(float64)
	_ = toolName
	return int(statusF), isErr
}

// ---------- HTTP-side helpers used by the parity assertions. ----------

// requestStatusAuth issues a verb+path request with the bearer (as
// Authorization) and an empty body for non-GET methods, returning the
// HTTP status only. Used to assert the auth gate's deny shape on the
// HTTP route against the MCP-side deny.
func requestStatusAuth(t *testing.T, ep harness.RimskyEndpoint, verb, path, bearer string, body any) int {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("requestStatusAuth: marshal body for %s %s: %v", verb, path, err)
		}
		bodyReader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(verb, ep.BaseURL+path, bodyReader)
	if err != nil {
		t.Fatalf("requestStatusAuth: build %s %s: %v", verb, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	// Universal write idempotency header — harmless on routes that
	// don't consult it, required on POST /instances/{id}/messages.
	if verb == http.MethodPost || verb == http.MethodPut {
		req.Header.Set("Idempotency-Key", "mcp-parity-"+uuid.NewString())
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("requestStatusAuth: %s %s: %v", verb, path, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// getJSONAuth is GetJSON + bearer convenience for parity assertions
// that need both the status and the body to do a shape comparison.
func getJSONAuth(t *testing.T, ep harness.RimskyEndpoint, path, bearer string) (int, []byte) {
	t.Helper()
	return ep.GetJSON(t, path, bearer)
}

// createInstanceAuth POSTs /v1/instances with the supplied bearer
// and returns the new instance id. Mirrors createSQLiteInstance but
// authenticated.
func createInstanceAuth(t *testing.T, ep harness.RimskyEndpoint, bearer, templateID, instanceKey string, params map[string]any) string {
	t.Helper()
	status, raw := ep.PostJSONWithHeaders(t, "/v1/instances", map[string]any{
		"template":     templateID,
		"instance_key": instanceKey,
		"params":       params,
	}, map[string]string{"Authorization": "Bearer " + bearer})
	if status != http.StatusCreated {
		t.Fatalf("POST /v1/instances: %d %s", status, string(raw))
	}
	var resp struct {
		InstanceID string `json:"instance_id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode instance response: %v: %s", err, string(raw))
	}
	if resp.InstanceID == "" {
		t.Fatalf("instance_id empty: %s", string(raw))
	}
	return resp.InstanceID
}

// waitForFirstNodeID polls the instance's node-list until at least one
// node row exists, then returns its id. Required because the scheduler
// creates the worker node asynchronously after the instance is
// persisted; the parity test needs the node id to seed node-category
// tool args.
func waitForFirstNodeID(t *testing.T, ep harness.RimskyEndpoint, bearer, instanceID string, deadline time.Duration) string {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		status, raw := getJSONAuth(t, ep, "/v1/instances/"+instanceID+"/nodes", bearer)
		if status == http.StatusOK {
			var resp struct {
				Nodes []struct {
					ID string `json:"id"`
				} `json:"nodes"`
			}
			if err := json.Unmarshal(raw, &resp); err == nil && len(resp.Nodes) > 0 {
				return resp.Nodes[0].ID
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("waitForFirstNodeID: no nodes appeared on instance %s within %v", instanceID, deadline)
	return ""
}

// assertBreakpointAppears polls the breakpoint list until a row with
// the named checkpoint appears. Bounds the wait so a real MCP-write
// failure surfaces as the parity defect rather than a silent timeout.
func assertBreakpointAppears(t *testing.T, ep harness.RimskyEndpoint, bearer, idOrKey, checkpoint string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		status, raw := getJSONAuth(t, ep, "/v1/instances/"+idOrKey+"/breakpoints", bearer)
		if status == http.StatusOK && strings.Contains(string(raw), `"`+checkpoint+`"`) {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	status, raw := getJSONAuth(t, ep, "/v1/instances/"+idOrKey+"/breakpoints", bearer)
	t.Fatalf("breakpoint mutation parity: list missing %s breakpoint after MCP breakpoint_create within %v\nfinal status: %d\nbody: %s",
		checkpoint, deadline, status, string(raw))
}

// assertOperatorOverrideEvent polls the instance event log for a
// recent `message_emitted` event (the runtime's InvalidateNode appends
// one when an operator override is applied). The mutation parity for
// the node category requires the MCP-driven invalidate to land a real
// event — a canned MCP success would leave no event on the log.
func assertOperatorOverrideEvent(t *testing.T, ep harness.RimskyEndpoint, bearer, instanceID string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		status, raw := getJSONAuth(t, ep, "/v1/events?instance_id="+instanceID, bearer)
		if status == http.StatusOK {
			if strings.Contains(string(raw), `"operator_override"`) || strings.Contains(string(raw), `"message_emitted"`) {
				return
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("assertOperatorOverrideEvent: no operator_override / message_emitted event for instance %s within %v — node_invalidate via MCP did not trigger the real handler", instanceID, deadline)
}

// instanceFromNode reads a node by id (route gated by node:read) and
// returns its instance id, used by the node-category parity assertion
// to look up the event log without smuggling state through the
// category struct.
func instanceFromNode(t *testing.T, ep harness.RimskyEndpoint, bearer, nodeID string) string {
	t.Helper()
	status, raw := getJSONAuth(t, ep, "/v1/nodes/"+nodeID, bearer)
	if status != http.StatusOK {
		t.Fatalf("instanceFromNode: GET /v1/nodes/%s returned %d, want 200\nbody: %s", nodeID, status, string(raw))
	}
	var resp struct {
		InstanceID string `json:"instance_id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("instanceFromNode: decode %s: %v", raw, err)
	}
	return resp.InstanceID
}

// adminBearer is an identity-helper for parity assertions that need to
// emphasize they are using the admin key (vs the mcp-only key) at the
// call site. Today it's a pass-through; it exists so the test code reads
// cleanly without inline comments.
func adminBearer(t *testing.T, bearer string) string {
	t.Helper()
	return bearer
}

// toolNames extracts tool names from a tools/list result for failure
// messages.
func toolNames(tools []toolEntry) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.Name)
	}
	return out
}

// jsonMapKeys returns the keys of a JSON-decoded map for failure
// messages.
func jsonMapKeys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
